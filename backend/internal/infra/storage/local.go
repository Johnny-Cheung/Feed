package storage

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var (
	// ErrEmptyFile 表示上传内容为空。
	ErrEmptyFile = errors.New("file is empty")
	// ErrInvalidUpload 表示调用方传入了无效的文件对象或文件头。
	ErrInvalidUpload = errors.New("invalid upload")
	// ErrFileTooLarge 表示文件超过当前类型允许的大小上限。
	ErrFileTooLarge = errors.New("file too large")
	// ErrUnsupportedFileExtension 表示扩展名不在白名单内。
	ErrUnsupportedFileExtension = errors.New("unsupported file extension")
	// ErrUnsupportedMIMEType 表示文件内容探测出的 MIME 类型不在白名单内。
	ErrUnsupportedMIMEType = errors.New("unsupported mime type")
)

// Kind 用来区分当前保存的是哪一类文件。
// 不同类型会套用不同的目录、大小限制和白名单。
type Kind string

const (
	KindVideo  Kind = "video"
	KindCover  Kind = "cover"
	KindAvatar Kind = "avatar"
)

type Config struct {
	RootDir    string
	VideosDir  string
	CoversDir  string
	AvatarsDir string
	MaxVideoMB int
	MaxImageMB int
}

// SaveResult 是文件落盘成功后返回给业务层的结果。
// 这里刻意只返回相对路径，不返回绝对路径。
// 这样后续如果从本地磁盘切换到对象存储，业务层改动会更小。
type SaveResult struct {
	RelativePath string
	SizeBytes    uint64
}

// StaticMount 描述“一个 URL 前缀应该映射到哪个磁盘目录”。
// 路由层会据此批量注册静态资源访问路径。
type StaticMount struct {
	URLPrefix string
	Directory string
}

// LocalStorage 是阶段五实现的本地文件存储服务。
// 它负责三件事：
// 1. 创建目录
// 2. 校验上传文件
// 3. 把文件保存到本地磁盘
type LocalStorage struct {
	cfg Config
}

// savePolicy 是保存某一类文件时用到的规则集合。
// 例如视频和图片的大小限制、允许的扩展名和 MIME 都不同。
type savePolicy struct {
	subDir       string
	maxBytes     int64
	allowedTypes map[string]map[string]struct{}
}

// NewLocalStorage 创建本地存储服务，并在启动阶段预先把目录建好。
// 这样后续业务真正保存文件时，就不需要再关心顶层目录是否存在。
func NewLocalStorage(cfg Config) (*LocalStorage, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	storage := &LocalStorage{cfg: cfg}
	for _, dir := range storage.directories() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create storage directory %q: %w", dir, err)
		}
	}

	return storage, nil
}

// SaveVideo 保存视频文件。
// 当前只允许 mp4。
func (s *LocalStorage) SaveVideo(file multipart.File, header *multipart.FileHeader) (*SaveResult, error) {
	return s.saveUploadedFile(KindVideo, file, header)
}

// SaveCover 保存视频封面。
func (s *LocalStorage) SaveCover(file multipart.File, header *multipart.FileHeader) (*SaveResult, error) {
	return s.saveUploadedFile(KindCover, file, header)
}

// SaveAvatar 保存用户头像。
func (s *LocalStorage) SaveAvatar(file multipart.File, header *multipart.FileHeader) (*SaveResult, error) {
	return s.saveUploadedFile(KindAvatar, file, header)
}

// Delete 用于删除一个已经落盘的相对路径文件。
// 当前阶段它主要用于“发布/更新失败时的回滚清理”。
func (s *LocalStorage) Delete(relativePath string) error {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return nil
	}

	absolutePath := filepath.Join(s.cfg.RootDir, filepath.FromSlash(relativePath))
	if err := os.Remove(absolutePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove storage file: %w", err)
	}

	return nil
}

// StaticMounts 返回阶段五需要暴露出去的静态目录映射。
// 这样 router 层不需要知道 storage 的内部细节，只要遍历注册即可。
func (s *LocalStorage) StaticMounts() []StaticMount {
	return []StaticMount{
		{
			URLPrefix: path.Join("/static", s.cfg.VideosDir),
			Directory: filepath.Join(s.cfg.RootDir, s.cfg.VideosDir),
		},
		{
			URLPrefix: path.Join("/static", s.cfg.CoversDir),
			Directory: filepath.Join(s.cfg.RootDir, s.cfg.CoversDir),
		},
		{
			URLPrefix: path.Join("/static", s.cfg.AvatarsDir),
			Directory: filepath.Join(s.cfg.RootDir, s.cfg.AvatarsDir),
		},
	}
}

// BuildStaticURL 把数据库里的相对路径组装成最终对外可访问的 URL。
// 例如：
// staticBaseURL = http://localhost:18080
// relativePath  = avatars/2026/04/24/a.png
// 最终得到：
// http://localhost:18080/static/avatars/2026/04/24/a.png
func BuildStaticURL(staticBaseURL, relativePath string) string {
	relativePath = strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(relativePath)), "/")
	if relativePath == "" {
		return ""
	}

	urlPath := path.Join("/static", relativePath)
	if strings.TrimSpace(staticBaseURL) == "" {
		return urlPath
	}

	return strings.TrimRight(staticBaseURL, "/") + urlPath
}

// saveUploadedFile 是三类文件保存逻辑的统一实现。
// 它的处理流程是：
// 1. 选择当前文件类型对应的策略
// 2. 校验扩展名
// 3. 探测 MIME 并校验
// 4. 生成“日期目录 + UUID 文件名”的相对路径
// 5. 将文件写入磁盘
// 6. 返回相对路径和大小
func (s *LocalStorage) saveUploadedFile(kind Kind, file multipart.File, header *multipart.FileHeader) (*SaveResult, error) {
	// 第一步：先判断调用方传进来的文件对象和文件头是否有效。
	// 如果这里就是 nil，后面任何读取、取文件名的动作都会直接 panic 或报错，
	// 所以要尽早拦住。
	if file == nil || header == nil {
		return nil, ErrInvalidUpload
	}

	// 第二步：根据当前文件类型（视频/封面/头像）拿到对应的保存策略。
	// 这个策略里包含：
	// - 要落到哪个子目录
	// - 允许的最大大小
	// - 允许的扩展名和 MIME 白名单
	policy, err := s.policy(kind)
	if err != nil {
		return nil, err
	}

	// 第三步：从原始文件名里取出扩展名，并统一转成小写。
	// 例如：
	// avatar.PNG -> .png
	// movie.MP4 -> .mp4
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(header.Filename)))

	// 如果连扩展名都没有，直接拒绝。
	// 因为当前阶段的白名单就是依赖扩展名 + MIME 双重校验。
	if ext == "" {
		return nil, fmt.Errorf("%w: missing extension", ErrUnsupportedFileExtension)
	}

	// 第四步：先根据扩展名检查一次白名单。
	// 这里的 allowedMIMEs 表示：
	// “当前这个扩展名”允许对应哪些 MIME 类型。
	// 例如 .png 允许 image/png。
	allowedMIMEs, ok := policy.allowedTypes[ext]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFileExtension, ext)
	}

	// 第五步：如果 multipart 头里已经给出了文件大小，
	// 那就先做一轮快速大小校验。
	// 这一步的目的主要是“尽早失败”，避免无意义地继续读文件。
	if header.Size > 0 && header.Size > policy.maxBytes {
		return nil, fmt.Errorf("%w: max=%d actual=%d", ErrFileTooLarge, policy.maxBytes, header.Size)
	}

	// 第六步：读取文件前 512 字节。
	// 这是一种很常见的做法，因为标准库的 http.DetectContentType
	// 就是通过前面这部分字节去猜测文件真实类型。
	//
	// 为什么要这样做？
	// 因为只看扩展名不安全。
	// 例如用户完全可以把一个非图片文件改名成 .png，
	// 但文件内容本身骗不过 MIME 探测。
	sniffBuffer := make([]byte, 512)
	readBytes, err := io.ReadFull(file, sniffBuffer)

	// io.ReadFull 在这里读不满 512 字节并不一定是错误，
	// 小文件可能直接 EOF 或 ErrUnexpectedEOF。
	// 真正要拦住的是“除了这两种以外的读取失败”。
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("read upload header: %w", err)
	}

	// 如果一个字节都没读到，说明这就是空文件。
	if readBytes == 0 {
		return nil, ErrEmptyFile
	}

	// 第七步：根据刚才读到的文件头内容探测 MIME。
	// 例如可能探测出：
	// - image/png
	// - image/jpeg
	// - video/mp4
	detectedMIME := http.DetectContentType(sniffBuffer[:readBytes])

	// 再用 MIME 做第二轮白名单校验。
	// 这样就形成了：
	// “扩展名 + 文件内容探测 MIME”
	// 的双重限制。
	if _, ok := allowedMIMEs[detectedMIME]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMIMEType, detectedMIME)
	}

	// 第八步：生成相对路径。
	// 这个相对路径是要写入数据库的，不是绝对磁盘路径。
	// 例如：
	// avatars/2026/04/24/uuid.png
	relativePath, err := buildRelativePath(policy.subDir, ext, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("build relative path: %w", err)
	}

	// 再把相对路径拼成真正的磁盘绝对路径。
	// 例如：
	// RootDir = ./storage
	// RelativePath = avatars/2026/04/24/uuid.png
	// AbsolutePath = ./storage/avatars/2026/04/24/uuid.png
	absolutePath := filepath.Join(s.cfg.RootDir, filepath.FromSlash(relativePath))

	// 第九步：确保最终文件所在的父目录存在。
	// 注意这里不是创建文件本身，而是创建它所在的年月日目录。
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return nil, fmt.Errorf("create parent directory: %w", err)
	}

	// 第十步：以“新建文件”的方式打开目标文件。
	// 这里用了 O_EXCL，表示如果文件已存在就失败，
	// 避免不小心覆盖已有文件。
	destination, err := os.OpenFile(absolutePath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}

	// 第十一步：把“已经读出来做 MIME 探测的那部分字节”
	// 和“文件剩余还没读的部分”重新拼接起来。
	//
	// 为什么要这样做？
	// 因为 file 是流式读取的。
	// 前面读了 512 字节以后，文件指针已经往后走了。
	// 如果这里直接继续 io.Copy(file, ...) ，前 512 字节就丢了。
	// 所以要用 MultiReader 把它们拼回去，保证写出的文件完整。
	reader := io.MultiReader(bytes.NewReader(sniffBuffer[:readBytes]), file)

	// 第十二步：再套一层 LimitedReader 做“真正写入时”的大小兜底。
	// 即使 header.Size 不可信，或者客户端故意乱填，
	// 这里也能在写磁盘时再次卡住超大文件。
	//
	// 注意这里是 maxBytes + 1：
	// 这样一旦多读出 1 个字节，就说明文件超限了。
	limitedReader := &io.LimitedReader{R: reader, N: policy.maxBytes + 1}

	// 第十三步：把数据从 reader 写入目标文件。
	written, copyErr := io.Copy(destination, limitedReader)

	// 无论写成功还是失败，都要把目标文件句柄关闭掉。
	closeErr := destination.Close()

	// 如果写入过程失败，说明文件内容没完整落盘。
	// 这时要把已经创建出来的半成品文件删掉，避免留下脏数据。
	if copyErr != nil {
		_ = os.Remove(absolutePath)
		return nil, fmt.Errorf("write file: %w", copyErr)
	}

	// 如果关闭文件失败，也把文件删掉。
	// 原因类似：我们希望“要么完整成功，要么不留下残缺结果”。
	if closeErr != nil {
		_ = os.Remove(absolutePath)
		return nil, fmt.Errorf("close file: %w", closeErr)
	}

	// 第十四步：检查最终写入大小是否超限。
	// 因为前面 LimitedReader 放宽到了 maxBytes + 1，
	// 所以只要 written > maxBytes，就说明文件真实大小超了。
	// 这时同样要删掉已经写出来的文件。
	if written > policy.maxBytes {
		_ = os.Remove(absolutePath)
		return nil, fmt.Errorf("%w: max=%d actual=%d", ErrFileTooLarge, policy.maxBytes, written)
	}

	// 第十五步：返回结果给业务层。
	// 业务层通常只需要：
	// - 数据库里该存什么相对路径
	// - 文件实际大小是多少
	return &SaveResult{
		RelativePath: relativePath,
		SizeBytes:    uint64(written),
	}, nil
}

// directories 返回启动时需要确保存在的目录列表。
func (s *LocalStorage) directories() []string {
	return []string{
		s.cfg.RootDir,
		filepath.Join(s.cfg.RootDir, s.cfg.VideosDir),
		filepath.Join(s.cfg.RootDir, s.cfg.CoversDir),
		filepath.Join(s.cfg.RootDir, s.cfg.AvatarsDir),
	}
}

// policy 根据文件类型返回对应的保存策略。
// 例如：
// - 视频走 videos 目录，大小用 MaxVideoMB
// - 封面和头像走图片白名单，大小用 MaxImageMB
func (s *LocalStorage) policy(kind Kind) (savePolicy, error) {
	switch kind {
	case KindVideo:
		return savePolicy{
			subDir:   s.cfg.VideosDir,
			maxBytes: int64(s.cfg.MaxVideoMB) * 1024 * 1024,
			allowedTypes: map[string]map[string]struct{}{
				".mp4": {"video/mp4": {}},
			},
		}, nil
	case KindCover:
		return savePolicy{
			subDir:       s.cfg.CoversDir,
			maxBytes:     int64(s.cfg.MaxImageMB) * 1024 * 1024,
			allowedTypes: allowedImageTypes(),
		}, nil
	case KindAvatar:
		return savePolicy{
			subDir:       s.cfg.AvatarsDir,
			maxBytes:     int64(s.cfg.MaxImageMB) * 1024 * 1024,
			allowedTypes: allowedImageTypes(),
		}, nil
	default:
		return savePolicy{}, fmt.Errorf("unsupported storage kind: %s", kind)
	}
}

// validateConfig 负责在服务启动时尽早发现存储配置错误。
func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.RootDir) == "" {
		return errors.New("storage root dir is required")
	}
	if strings.TrimSpace(cfg.VideosDir) == "" || strings.TrimSpace(cfg.CoversDir) == "" || strings.TrimSpace(cfg.AvatarsDir) == "" {
		return errors.New("storage sub directories are required")
	}
	if cfg.MaxVideoMB <= 0 {
		return errors.New("storage max video size must be positive")
	}
	if cfg.MaxImageMB <= 0 {
		return errors.New("storage max image size must be positive")
	}
	return nil
}

// allowedImageTypes 返回图片扩展名和 MIME 白名单。
// 这里用 map[string]map[string]struct{} 的原因是：
// 一个扩展名未来可能允许多个 MIME。
func allowedImageTypes() map[string]map[string]struct{} {
	return map[string]map[string]struct{}{
		".jpg":  {"image/jpeg": {}},
		".jpeg": {"image/jpeg": {}},
		".png":  {"image/png": {}},
		".webp": {"image/webp": {}},
	}
}

// buildRelativePath 生成数据库里要保存的相对路径。
// 路径规则采用：
// 子目录 / 年 / 月 / 日 / uuid.扩展名
func buildRelativePath(subDir, extension string, now time.Time) (string, error) {
	dateDir := now.Format("2006/01/02")
	uuid, err := newUUID()
	if err != nil {
		return "", err
	}
	fileName := uuid + extension
	return path.Join(subDir, dateDir, fileName), nil
}

// newUUID 生成一个 UUID v4 风格的随机文件名。
// 这里不依赖第三方库，直接用随机字节自行组装。
func newUUID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		buffer[0:4],
		buffer[4:6],
		buffer[6:8],
		buffer[8:10],
		buffer[10:16],
	), nil
}
