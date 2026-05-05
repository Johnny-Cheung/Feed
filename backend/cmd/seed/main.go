package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type options struct {
	BaseURL                string
	ExistingAuthorUsername string
	ExistingAuthorPassword string
	ExistingViewerUsername string
	ExistingViewerPassword string
	AuthorPrefix           string
	ViewerPrefix           string
	AuthorPassword         string
	ViewerPassword         string
	AuthorCount            int
	ViewerCount            int
	VideosPerAuthor        int
	FollowPerViewer        int
	LikePerViewer          int
	FavoritePerViewer      int
	CommentPerViewer       int
	SummaryPath            string
	VideoSamplePath        string
	CoverSamplePath        string
}

type apiClient struct {
	baseURL string
	client  *http.Client
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

type currentUserResponse struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
}

type videoResponse struct {
	ID uint64 `json:"id"`
}

type commentResponse struct {
	ID uint64 `json:"id"`
}

type userAccount struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"-"`
}

type authorSummary struct {
	ID       uint64   `json:"id"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	VideoIDs []uint64 `json:"video_ids"`
}

type viewerSummary struct {
	ID              uint64   `json:"id"`
	Username        string   `json:"username"`
	Password        string   `json:"password"`
	FollowAuthorIDs []uint64 `json:"follow_author_ids"`
}

type seedSummary struct {
	GeneratedAt string `json:"generated_at"`
	BaseURL     string `json:"base_url"`
	SampleFiles struct {
		VideoPath string `json:"video_path"`
		CoverPath string `json:"cover_path"`
	} `json:"sample_files"`
	Authors  []authorSummary `json:"authors"`
	Viewers  []viewerSummary `json:"viewers"`
	VideoIDs []uint64        `json:"video_ids"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "seed failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return errors.New("base-url is required")
	}

	backendRoot, err := findBackendRoot()
	if err != nil {
		return err
	}

	videoSample, err := resolveSamplePath(cfg.VideoSamplePath, filepath.Join(backendRoot, "storage", "videos"), []string{".mp4"})
	if err != nil {
		return fmt.Errorf("find sample video: %w", err)
	}

	coverSample, err := resolveSamplePath(cfg.CoverSamplePath, filepath.Join(backendRoot, "storage", "covers"), []string{".png", ".jpg", ".jpeg", ".webp"})
	if err != nil {
		return fmt.Errorf("find sample cover: %w", err)
	}

	summaryPath := cfg.SummaryPath
	if !filepath.IsAbs(summaryPath) {
		summaryPath = filepath.Join(backendRoot, summaryPath)
	}

	client := &apiClient{
		baseURL: cfg.BaseURL,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	fmt.Printf("Using sample video: %s\n", videoSample)
	fmt.Printf("Using sample cover: %s\n", coverSample)

	authors, viewers, err := prepareUsers(context.Background(), client, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Prepared %d authors and %d viewers\n", len(authors), len(viewers))

	allVideoIDs, videosByAuthor, err := publishVideos(context.Background(), client, cfg, authors, videoSample, coverSample)
	if err != nil {
		return err
	}

	viewerFollows, err := seedInteractions(context.Background(), client, cfg, rng, authors, viewers, allVideoIDs, videosByAuthor)
	if err != nil {
		return err
	}

	summary := buildSummary(cfg.BaseURL, videoSample, coverSample, authors, viewers, allVideoIDs, videosByAuthor, viewerFollows)
	if err := writeSummary(summaryPath, summary); err != nil {
		return err
	}

	fmt.Printf("Seed summary written to %s\n", summaryPath)
	fmt.Printf("Total authors: %d\n", len(summary.Authors))
	fmt.Printf("Total viewers: %d\n", len(summary.Viewers))
	fmt.Printf("Total videos: %d\n", len(summary.VideoIDs))
	return nil
}

func parseFlags() options {
	cfg := options{}
	flag.StringVar(&cfg.BaseURL, "base-url", "http://localhost:18080", "API base URL")
	flag.StringVar(&cfg.ExistingAuthorUsername, "existing-author-username", "author001", "existing author username")
	flag.StringVar(&cfg.ExistingAuthorPassword, "existing-author-password", "1234567", "existing author password")
	flag.StringVar(&cfg.ExistingViewerUsername, "existing-viewer-username", "viewer001", "existing viewer username")
	flag.StringVar(&cfg.ExistingViewerPassword, "existing-viewer-password", "123456", "existing viewer password")
	flag.StringVar(&cfg.AuthorPrefix, "author-prefix", "lt_author", "generated author username prefix")
	flag.StringVar(&cfg.ViewerPrefix, "viewer-prefix", "lt_viewer", "generated viewer username prefix")
	flag.StringVar(&cfg.AuthorPassword, "author-password", "1234567", "generated author password")
	flag.StringVar(&cfg.ViewerPassword, "viewer-password", "123456", "generated viewer password")
	flag.IntVar(&cfg.AuthorCount, "author-count", 19, "additional generated author count")
	flag.IntVar(&cfg.ViewerCount, "viewer-count", 99, "additional generated viewer count")
	flag.IntVar(&cfg.VideosPerAuthor, "videos-per-author", 10, "videos to publish for each author")
	flag.IntVar(&cfg.FollowPerViewer, "follow-per-viewer", 3, "authors each viewer follows")
	flag.IntVar(&cfg.LikePerViewer, "like-per-viewer", 10, "videos each viewer likes")
	flag.IntVar(&cfg.FavoritePerViewer, "favorite-per-viewer", 6, "videos each viewer favorites")
	flag.IntVar(&cfg.CommentPerViewer, "comment-per-viewer", 4, "comments each viewer creates")
	flag.StringVar(&cfg.SummaryPath, "summary-path", "scripts/loadtest/seed-output.json", "seed summary output path, relative to backend root when not absolute")
	flag.StringVar(&cfg.VideoSamplePath, "video-sample", "", "sample .mp4 path; defaults to first file under storage/videos")
	flag.StringVar(&cfg.CoverSamplePath, "cover-sample", "", "sample cover image path; defaults to first image under storage/covers")
	flag.Parse()
	return cfg
}

func prepareUsers(ctx context.Context, client *apiClient, cfg options) ([]userAccount, []userAccount, error) {
	authors := make([]userAccount, 0, cfg.AuthorCount+1)
	viewers := make([]userAccount, 0, cfg.ViewerCount+1)

	existingAuthor, err := client.ensureUser(ctx, cfg.ExistingAuthorUsername, cfg.ExistingAuthorPassword)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare existing author: %w", err)
	}
	authors = append(authors, existingAuthor)

	existingViewer, err := client.ensureUser(ctx, cfg.ExistingViewerUsername, cfg.ExistingViewerPassword)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare existing viewer: %w", err)
	}
	viewers = append(viewers, existingViewer)

	for i := 1; i <= cfg.AuthorCount; i++ {
		username := fmt.Sprintf("%s_%03d", cfg.AuthorPrefix, i)
		author, err := client.ensureUser(ctx, username, cfg.AuthorPassword)
		if err != nil {
			return nil, nil, fmt.Errorf("prepare author %s: %w", username, err)
		}
		authors = append(authors, author)
	}

	for i := 1; i <= cfg.ViewerCount; i++ {
		username := fmt.Sprintf("%s_%03d", cfg.ViewerPrefix, i)
		viewer, err := client.ensureUser(ctx, username, cfg.ViewerPassword)
		if err != nil {
			return nil, nil, fmt.Errorf("prepare viewer %s: %w", username, err)
		}
		viewers = append(viewers, viewer)
	}

	return authors, viewers, nil
}

func publishVideos(ctx context.Context, client *apiClient, cfg options, authors []userAccount, videoSample, coverSample string) ([]uint64, map[uint64][]uint64, error) {
	allVideoIDs := make([]uint64, 0, len(authors)*cfg.VideosPerAuthor)
	videosByAuthor := make(map[uint64][]uint64, len(authors))

	for _, author := range authors {
		authorVideoIDs := make([]uint64, 0, cfg.VideosPerAuthor)
		for i := 1; i <= cfg.VideosPerAuthor; i++ {
			title := fmt.Sprintf("seed-%s-%d", author.Username, i)
			video, err := client.publishVideo(ctx, author.Token, title, videoSample, coverSample)
			if err != nil {
				return nil, nil, fmt.Errorf("publish video for %s: %w", author.Username, err)
			}
			authorVideoIDs = append(authorVideoIDs, video.ID)
			allVideoIDs = append(allVideoIDs, video.ID)
			fmt.Printf("Published video %d for %s\n", video.ID, author.Username)
		}
		videosByAuthor[author.ID] = authorVideoIDs
	}

	return allVideoIDs, videosByAuthor, nil
}

func seedInteractions(ctx context.Context, client *apiClient, cfg options, rng *rand.Rand, authors, viewers []userAccount, allVideoIDs []uint64, videosByAuthor map[uint64][]uint64) (map[uint64][]uint64, error) {
	viewerFollows := make(map[uint64][]uint64, len(viewers))

	for _, viewer := range viewers {
		candidates := make([]userAccount, 0, len(authors))
		for _, author := range authors {
			if author.ID != viewer.ID {
				candidates = append(candidates, author)
			}
		}

		followAuthors := randomSubset(rng, candidates, cfg.FollowPerViewer)
		followAuthorIDs := make([]uint64, 0, len(followAuthors))
		for _, author := range followAuthors {
			if err := client.emptyPost(ctx, fmt.Sprintf("/api/v1/users/%d/follow", author.ID), viewer.Token); err != nil {
				return nil, fmt.Errorf("viewer %s follow author %d: %w", viewer.Username, author.ID, err)
			}
			followAuthorIDs = append(followAuthorIDs, author.ID)
		}
		viewerFollows[viewer.ID] = followAuthorIDs

		candidateVideoIDs := make([]uint64, 0)
		for _, authorID := range followAuthorIDs {
			candidateVideoIDs = append(candidateVideoIDs, videosByAuthor[authorID]...)
		}
		if len(candidateVideoIDs) == 0 {
			candidateVideoIDs = append(candidateVideoIDs, allVideoIDs...)
		}

		for _, videoID := range randomSubset(rng, candidateVideoIDs, cfg.LikePerViewer) {
			if err := client.emptyPost(ctx, fmt.Sprintf("/api/v1/videos/%d/likes", videoID), viewer.Token); err != nil {
				return nil, fmt.Errorf("viewer %s like video %d: %w", viewer.Username, videoID, err)
			}
		}
		for _, videoID := range randomSubset(rng, candidateVideoIDs, cfg.FavoritePerViewer) {
			if err := client.emptyPost(ctx, fmt.Sprintf("/api/v1/videos/%d/favorites", videoID), viewer.Token); err != nil {
				return nil, fmt.Errorf("viewer %s favorite video %d: %w", viewer.Username, videoID, err)
			}
		}
		for _, videoID := range randomSubset(rng, candidateVideoIDs, cfg.CommentPerViewer) {
			content := fmt.Sprintf("seed-comment-%s-%d-%d", viewer.Username, videoID, rng.Intn(9000)+1000)
			if _, err := client.createComment(ctx, viewer.Token, videoID, content); err != nil {
				return nil, fmt.Errorf("viewer %s comment video %d: %w", viewer.Username, videoID, err)
			}
		}
	}

	return viewerFollows, nil
}

func buildSummary(baseURL, videoSample, coverSample string, authors, viewers []userAccount, allVideoIDs []uint64, videosByAuthor map[uint64][]uint64, viewerFollows map[uint64][]uint64) seedSummary {
	summary := seedSummary{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		BaseURL:     baseURL,
		Authors:     make([]authorSummary, 0, len(authors)),
		Viewers:     make([]viewerSummary, 0, len(viewers)),
		VideoIDs:    append([]uint64(nil), allVideoIDs...),
	}
	summary.SampleFiles.VideoPath = videoSample
	summary.SampleFiles.CoverPath = coverSample

	for _, author := range authors {
		summary.Authors = append(summary.Authors, authorSummary{
			ID:       author.ID,
			Username: author.Username,
			Password: author.Password,
			VideoIDs: append([]uint64(nil), videosByAuthor[author.ID]...),
		})
	}

	for _, viewer := range viewers {
		summary.Viewers = append(summary.Viewers, viewerSummary{
			ID:              viewer.ID,
			Username:        viewer.Username,
			Password:        viewer.Password,
			FollowAuthorIDs: append([]uint64(nil), viewerFollows[viewer.ID]...),
		})
	}

	return summary
}

func writeSummary(summaryPath string, summary seedSummary) error {
	if err := os.MkdirAll(filepath.Dir(summaryPath), 0o755); err != nil {
		return fmt.Errorf("create summary directory: %w", err)
	}

	payload, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	payload = append(payload, '\n')

	if err := os.WriteFile(summaryPath, payload, 0o644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func (c *apiClient) ensureUser(ctx context.Context, username, password string) (userAccount, error) {
	token, err := c.login(ctx, username, password)
	if err != nil {
		if registerErr := c.register(ctx, username, password); registerErr != nil {
			return userAccount{}, fmt.Errorf("login failed (%v), then register failed: %w", err, registerErr)
		}
		token, err = c.login(ctx, username, password)
		if err != nil {
			return userAccount{}, fmt.Errorf("login after register: %w", err)
		}
	}

	me, err := c.me(ctx, token.AccessToken)
	if err != nil {
		return userAccount{}, err
	}

	return userAccount{
		ID:       me.ID,
		Username: username,
		Password: password,
		Token:    token.AccessToken,
	}, nil
}

func (c *apiClient) login(ctx context.Context, username, password string) (tokenResponse, error) {
	var token tokenResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, "", &token)
	return token, err
}

func (c *apiClient) register(ctx context.Context, username, password string) error {
	var token tokenResponse
	return c.doJSON(ctx, http.MethodPost, "/api/v1/auth/register", map[string]string{
		"username": username,
		"password": password,
	}, "", &token)
}

func (c *apiClient) me(ctx context.Context, token string) (currentUserResponse, error) {
	var me currentUserResponse
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/auth/me", nil, token, &me)
	return me, err
}

func (c *apiClient) publishVideo(ctx context.Context, token, title, videoPath, coverPath string) (videoResponse, error) {
	var video videoResponse
	err := c.doMultipart(ctx, "/api/v1/videos", map[string]string{
		"title": title,
	}, map[string]string{
		"video": videoPath,
		"cover": coverPath,
	}, token, &video)
	return video, err
}

func (c *apiClient) createComment(ctx context.Context, token string, videoID uint64, content string) (commentResponse, error) {
	var comment commentResponse
	err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/videos/%d/comments", videoID), map[string]string{
		"content": content,
	}, token, &comment)
	return comment, err
}

func (c *apiClient) emptyPost(ctx context.Context, path, token string) error {
	return c.doJSON(ctx, http.MethodPost, path, nil, token, nil)
}

func (c *apiClient) doJSON(ctx context.Context, method, path string, body any, token string, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return c.do(req, out)
}

func (c *apiClient) doMultipart(ctx context.Context, path string, fields, files map[string]string, token string, out any) error {
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)

	go func() {
		err := writeMultipartPayload(writer, fields, files)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = pipeWriter.CloseWithError(err)
			return
		}
		_ = pipeWriter.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, pipeReader)
	if err != nil {
		_ = pipeReader.Close()
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return c.do(req, out)
}

func (c *apiClient) do(req *http.Request, out any) error {
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	rawBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s %s failed: status=%d body=%s", req.Method, req.URL.String(), res.StatusCode, string(rawBody))
	}

	var env envelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return fmt.Errorf("decode response envelope: %w body=%s", err, string(rawBody))
	}
	if env.Code != 0 {
		return fmt.Errorf("%s %s failed: code=%d message=%s", req.Method, req.URL.String(), env.Code, env.Message)
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode response data: %w", err)
	}
	return nil
}

func writeMultipartPayload(writer *multipart.Writer, fields, files map[string]string) error {
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return err
		}
	}

	for fieldName, filePath := range files {
		if err := writeMultipartFile(writer, fieldName, filePath); err != nil {
			return err
		}
	}
	return nil
}

func writeMultipartFile(writer *multipart.Writer, fieldName, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     fieldName,
		"filename": filepath.Base(filePath),
	}))
	header.Set("Content-Type", contentTypeForPath(filePath))

	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	return nil
}

func contentTypeForPath(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".mp4":
		return "video/mp4"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func randomSubset[T any](rng *rand.Rand, items []T, count int) []T {
	if len(items) == 0 || count <= 0 {
		return nil
	}
	if count > len(items) {
		count = len(items)
	}

	indexes := rng.Perm(len(items))[:count]
	result := make([]T, 0, count)
	for _, index := range indexes {
		result = append(result, items[index])
	}
	return result
}

func findBackendRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	current := wd
	for {
		goModPath := filepath.Join(current, "go.mod")
		payload, err := os.ReadFile(goModPath)
		if err == nil && strings.Contains(string(payload), "module feed-backend") {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return wd, nil
		}
		current = parent
	}
}

func resolveSamplePath(explicitPath, searchRoot string, allowedExts []string) (string, error) {
	if explicitPath != "" {
		absolutePath, err := filepath.Abs(explicitPath)
		if err != nil {
			return "", err
		}
		if err := ensureReadableFile(absolutePath); err != nil {
			return "", err
		}
		return absolutePath, nil
	}

	found, err := findFirstFile(searchRoot, allowedExts)
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("no sample file with extensions %s found under %s", strings.Join(allowedExts, ","), searchRoot)
	}
	return found, nil
}

func findFirstFile(root string, allowedExts []string) (string, error) {
	allowed := make(map[string]struct{}, len(allowedExts))
	for _, ext := range allowedExts {
		allowed[strings.ToLower(ext)] = struct{}{}
	}

	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if _, ok := allowed[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			return nil
		}
		if err := ensureReadableFile(path); err != nil {
			return err
		}
		found = path
		return filepath.SkipAll
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

func ensureReadableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}
