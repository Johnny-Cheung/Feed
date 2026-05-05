# Feed Backend

Feed Backend 是一个短视频信息流后端项目，提供账号认证、视频发布、首页热门流、关注流、点赞、收藏、评论、关注关系、用户主页和本地媒体文件访问能力。项目以 Go + Gin 为 HTTP 层，MySQL 保存主数据，Redis 承载 Feed 缓存与关系缓存，RabbitMQ 负责异步事件分发，适合作为短视频社区、内容流推荐或高并发读写链路的后端练习项目。

## 功能概览

- 用户账号：注册、登录、JWT 鉴权、当前用户信息、资料修改、头像上传、密码修改。
- 视频内容：视频发布、详情、标题/封面更新、软删除、本地媒体存储与静态访问。
- 信息流：匿名/登录首页热门流、登录用户关注流，均支持 cursor 分页。
- 互动体系：点赞、取消点赞、收藏、取消收藏、评论创建/删除/列表、关注/取关。
- 用户主页：作者主页、作者作品、我的作品、我的点赞、我的收藏、我的关注、我的粉丝。
- 异步链路：RabbitMQ topic exchange 分发 `video.*` 和 `user.*` 事件，后台消费者更新统计、热门 Feed、关注收件箱。
- 缓存体系：Redis 缓存视频基础信息、作者摘要、视频统计、观看者关系、首页热门集合、关注流收件箱、热门评论。
- 后台任务：定时对账统计、增量刷新热门 Feed、全量重建热门 Feed 与公共缓存。
- 压测脚本：提供种子数据生成、冒烟测试、读混合、写混合、读写混合 soak 测试。

## 技术栈

- Go `1.25.6`
- Gin
- Gorm
- MySQL `8.4`
- Redis `7.2`
- RabbitMQ `3.13-management`
- Viper
- JWT
- bcrypt
- k6
- Docker Compose

## 目录结构

```text
.
├── LICENSE
├── README.md
└── backend
    ├── cmd/server                 # HTTP 服务入口
    ├── configs                    # 配置样例和本地配置
    ├── deploy                     # Docker Compose 依赖编排
    ├── docs                       # 压测记录等项目文档
    ├── internal
    │   ├── app                    # 应用装配、启动和关闭
    │   ├── bootstrap              # 配置、MySQL、Redis、RabbitMQ、存储初始化
    │   ├── common                 # 统一响应、错误码、热度计算
    │   ├── domain                 # auth/feed/video/interaction/user 业务域
    │   ├── infra                  # Redis Feed 缓存、本地存储实现
    │   ├── jobs                   # 后台定时任务
    │   ├── middleware             # 鉴权、CORS、恢复、请求 ID
    │   ├── model                  # Gorm 模型与 AutoMigrate
    │   └── router                 # 路由装配
    ├── migrations                 # 预留迁移目录，当前使用 Gorm AutoMigrate
    ├── scripts
    │   ├── k6                     # k6 压测脚本
    │   └── loadtest               # 压测种子数据生成脚本
    └── storage                    # 本地上传文件目录，实际文件不进入 git
```

## 架构

```mermaid
flowchart LR
    Client["Client"] --> Gin["Gin HTTP API"]
    Gin --> Service["Domain Services"]
    Service --> MySQL["MySQL"]
    Service --> Redis["Redis Cache"]
    Service --> Storage["Local Storage"]
    Service --> RabbitMQ["RabbitMQ topic exchange"]
    RabbitMQ --> Consumers["Async Consumers"]
    Redis --> StreamConsumer["Redis Stream Consumer"]
    Consumers --> MySQL
    Consumers --> Redis
    StreamConsumer --> MySQL
    StreamConsumer --> Redis
    Jobs["Scheduled Jobs"] --> MySQL
    Jobs --> Redis
```

核心链路可以按下面的方式理解：

- HTTP 层只处理参数、鉴权和响应封装，业务规则下沉到各 domain service。
- MySQL 是最终一致的主存储，保存用户、视频、互动关系、评论和视频统计。
- Redis 用于读性能和实时互动体验，包括首页热门、有状态关系、关注收件箱和热门评论缓存。
- RabbitMQ 承载跨模块事件，发布视频、删除视频、评论、关注等动作会触发异步消费者刷新统计和 Feed。
- 后台任务用于兜底修复：即使异步链路短暂失败，也可以通过定时任务重建统计和缓存。

## 快速开始

### 1. 环境准备

需要安装：

- Go `1.25.6` 或兼容版本
- Docker 和 Docker Compose
- PowerShell，可选，用于运行种子数据脚本
- k6，可选，用于压测

### 2. 准备配置

服务启动时固定读取 `backend/configs/config.yaml`。该文件被 `.gitignore` 忽略，本地开发时从样例复制：

```powershell
cd D:\Feed\backend
Copy-Item .\configs\config.example.yaml .\configs\config.yaml
```

默认样例适合在宿主机运行 Go 服务，并通过 Docker 启动 MySQL、Redis、RabbitMQ：

- HTTP：`0.0.0.0:18080`
- MySQL：`127.0.0.1:13307`
- Redis：`127.0.0.1:6380`
- RabbitMQ：`amqp://guest:guest@127.0.0.1:5672/`
- RabbitMQ 管理页：`http://localhost:15672`

如需覆盖配置，可以使用 `FEED_` 前缀环境变量，配置层级用下划线表示。例如：

```powershell
$env:FEED_APP_PORT="18081"
$env:FEED_JWT_SECRET="replace-with-a-strong-secret"
```

### 3. 启动依赖

```powershell
cd D:\Feed\backend\deploy
docker compose up -d mysql redis rabbitmq
docker compose ps
```

`docker-compose.yml` 里也有一个 `app` 服务，但它当前是用于开发调试的 idle Go 容器，默认命令是 `sleep infinity`。本地开发推荐直接在宿主机执行 `go run`。

### 4. 启动后端

```powershell
cd D:\Feed\backend
go mod download
go run ./cmd/server
```

启动后会完成：

- 读取 `configs/config.yaml`
- 初始化本地存储目录
- 连接 MySQL、Redis、RabbitMQ
- 执行 Gorm AutoMigrate
- 声明 RabbitMQ exchange 和队列绑定
- 启动 RabbitMQ 消费者、Redis Stream 消费者和后台定时任务
- 注册 HTTP 路由并监听 `18080`

### 5. 健康检查

```powershell
Invoke-RestMethod http://localhost:18080/ping
Invoke-RestMethod http://localhost:18080/health
```

`/ping` 只验证 HTTP 服务可达。`/health` 会检查 MySQL、Redis、RabbitMQ，依赖异常时返回 `503`。

## 配置说明

主要配置段位于 `backend/configs/config.example.yaml`：

| 配置段 | 作用 |
| --- | --- |
| `app` | 服务名、环境、监听地址、超时、静态资源基础 URL |
| `mysql` | MySQL 连接、连接池和连接生命周期 |
| `redis` | Redis 地址、密码、DB |
| `rabbitmq` | AMQP URL、exchange、统计/热门/关注流/关注关系队列 |
| `jobs` | 后台任务开关、启动即运行、间隔、分布式锁 TTL、批大小 |
| `jwt` | JWT secret 和过期时间 |
| `storage` | 本地存储目录、视频/封面/头像子目录、上传大小限制 |
| `pagination` | 默认分页大小和最大分页大小 |
| `feed` | 关注流推拉模式阈值、首页热门容量、关系流长度、热门评论缓存 |
| `cors` | 允许跨域来源 |

本地上传文件会写入 `backend/storage` 下的 `videos`、`covers`、`avatars`。这些真实媒体文件默认不提交，只保留 `.gitkeep`。

## API 约定

### 统一响应

所有业务接口返回统一外层结构：

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

常用错误码：

| code | 含义 |
| --- | --- |
| `1001` | 参数错误 |
| `1002` | 未登录或 token 无效 |
| `1003` | 无权限 |
| `1004` | 资源不存在 |
| `2001` | 用户名已存在 |
| `2002` | 用户名或密码错误 |
| `2003` | 旧密码错误 |
| `4003` | 评论不存在 |
| `4004` | 不能删除他人评论 |
| `4005` | 不能关注自己 |
| `5000` | 内部错误 |
| `5001` | 依赖服务不可用 |

### 鉴权

登录和注册成功后会返回：

```json
{
  "access_token": "jwt-token",
  "expires_in": 86400
}
```

需要登录的接口在请求头携带：

```http
Authorization: Bearer <access_token>
```

### 分页

列表接口使用 `limit` 和 `cursor`：

- `limit` 为空时使用 `pagination.default_limit`
- `limit` 超过上限时会被限制到 `pagination.max_limit`
- `cursor` 是服务端返回的不透明游标，下一页原样带回即可

分页响应格式：

```json
{
  "items": [],
  "next_cursor": "",
  "has_more": false
}
```

### 静态资源

上传后的相对路径会通过 `app.static_base_url` 拼成可访问 URL：

- `/static/videos/...`
- `/static/covers/...`
- `/static/avatars/...`

当前上传限制：

- 视频：`.mp4`，MIME `video/mp4`
- 图片：`.jpg`、`.jpeg`、`.png`、`.webp`
- 大小上限由 `storage.max_video_mb` 和 `storage.max_image_mb` 控制

## 接口清单

### 系统

| 方法 | 路径 | 登录 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/ping` | 否 | HTTP 连通性检查 |
| `GET` | `/health` | 否 | 检查 app、MySQL、Redis、RabbitMQ |

### 认证

| 方法 | 路径 | 登录 | 请求 |
| --- | --- | --- | --- |
| `POST` | `/api/v1/auth/register` | 否 | JSON：`username`、`password` |
| `POST` | `/api/v1/auth/login` | 否 | JSON：`username`、`password` |
| `GET` | `/api/v1/auth/me` | 是 | 无 |

用户名长度为 `4-32`，密码长度为 `6-32`。

### Feed

| 方法 | 路径 | 登录 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/api/v1/feed/home?limit=&cursor=` | 可选 | 首页热门流，登录时补充 viewer_state |
| `GET` | `/api/v1/feed/following?limit=&cursor=` | 是 | 当前用户关注流 |

首页热门排序主要来自 Redis sorted set，MySQL 作为回源和兜底。关注流结合关注收件箱、作者 outbox 和 MySQL 回源，适配大作者推拉模式。

### 视频

| 方法 | 路径 | 登录 | 请求 |
| --- | --- | --- | --- |
| `POST` | `/api/v1/videos` | 是 | `multipart/form-data`：`title`、`video`、`cover` |
| `GET` | `/api/v1/videos/:video_id` | 可选 | 无 |
| `PUT` | `/api/v1/videos/:video_id` | 是 | `multipart/form-data`：可选 `title`、可选 `cover` |
| `DELETE` | `/api/v1/videos/:video_id` | 是 | 无 |

视频标题不能为空，最长 `100` 字符。更新和删除只能由作者本人操作。

视频卡片核心响应：

```json
{
  "id": 1,
  "title": "video title",
  "video_url": "http://localhost:18080/static/videos/...",
  "cover_url": "http://localhost:18080/static/covers/...",
  "published_at": "2026-05-05T00:00:00Z",
  "author": {
    "id": 1,
    "nickname": "author",
    "avatar_url": ""
  },
  "stats": {
    "like_count": 0,
    "comment_count": 0,
    "favorite_count": 0
  },
  "viewer_state": {
    "liked": false,
    "favorited": false,
    "following_author": false
  }
}
```

### 互动

| 方法 | 路径 | 登录 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/v1/videos/:video_id/likes` | 是 | 点赞 |
| `DELETE` | `/api/v1/videos/:video_id/likes` | 是 | 取消点赞 |
| `POST` | `/api/v1/videos/:video_id/favorites` | 是 | 收藏 |
| `DELETE` | `/api/v1/videos/:video_id/favorites` | 是 | 取消收藏 |
| `GET` | `/api/v1/videos/:video_id/comments?limit=&cursor=` | 否 | 评论列表 |
| `POST` | `/api/v1/videos/:video_id/comments` | 是 | JSON：`content` |
| `DELETE` | `/api/v1/comments/:comment_id` | 是 | 删除自己的评论 |
| `POST` | `/api/v1/users/:user_id/follow` | 是 | 关注用户 |
| `DELETE` | `/api/v1/users/:user_id/follow` | 是 | 取关用户 |

评论内容最长 `500` 字符。点赞、收藏和关注接口是幂等风格，重复操作会返回当前目标状态。

### 用户

| 方法 | 路径 | 登录 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/api/v1/users/me` | 是 | 当前用户资料 |
| `PUT` | `/api/v1/users/me/profile` | 是 | JSON：`nickname`、`bio` |
| `PUT` | `/api/v1/users/me/avatar` | 是 | `multipart/form-data`：`avatar` |
| `PUT` | `/api/v1/users/me/password` | 是 | JSON：`old_password`、`new_password` |
| `GET` | `/api/v1/users/me/videos?limit=&cursor=` | 是 | 我的作品 |
| `GET` | `/api/v1/users/me/liked-videos?limit=&cursor=` | 是 | 我点赞过的视频 |
| `GET` | `/api/v1/users/me/favorited-videos?limit=&cursor=` | 是 | 我收藏过的视频 |
| `GET` | `/api/v1/users/me/followings?limit=&cursor=` | 是 | 我的关注列表 |
| `GET` | `/api/v1/users/me/followers?limit=&cursor=` | 是 | 我的粉丝列表 |
| `GET` | `/api/v1/users/:user_id` | 可选 | 作者主页 |
| `GET` | `/api/v1/users/:user_id/videos?limit=&cursor=` | 可选 | 作者作品 |

用户关系状态 `relation_status` 可能为：

- `none`
- `following_author`
- `followed_by_author`
- `mutual`

匿名访问作者主页时 `relation_status` 为 `null`。

## 数据模型

当前通过 Gorm AutoMigrate 自动建表：

| 表 | 说明 |
| --- | --- |
| `users` | 账号、密码哈希、昵称、头像、简介、状态 |
| `videos` | 视频主数据、作者、标题、文件路径、状态、发布时间、软删除 |
| `video_stats` | 视频点赞数、评论数、收藏数、热度分 |
| `video_likes` | 用户点赞视频关系，用户和视频唯一 |
| `video_favorites` | 用户收藏视频关系，用户和视频唯一 |
| `comments` | 一级评论，支持软删除和状态过滤 |
| `user_follows` | 用户关注关系，用户和被关注用户唯一 |

热度分 V1 公式：

```text
(like_count + comment_count * 3 + favorite_count * 2) / published_days
```

其中 `published_days` 最小为 `1`。

## Redis 和 RabbitMQ

### Redis 主要职责

- 首页热门 sorted set：按热度分排序的视频集合。
- 热门脏数据集合：互动变化后标记待刷新视频，后台任务批量刷新。
- 视频基础信息、作者摘要、视频统计缓存：减少 Feed 组装时的 MySQL 查询。
- viewer relation 缓存：批量判断当前用户是否点赞、收藏、关注作者。
- 关注流 inbox/outbox：为普通作者做 fanout，为大作者保留 pull 模式 outbox。
- 热门评论缓存：热门视频优先读缓存，不命中再回源 MySQL。
- Redis Stream：高频点赞/收藏关系先写缓存和 stream，再由消费者落 MySQL。

### RabbitMQ 拓扑

配置样例默认使用 topic exchange：

```text
exchange: feed.events
```

队列绑定：

| 队列 | routing key | 作用 |
| --- | --- | --- |
| `video.stats.queue` | `video.*` | 对账互动关系并刷新 `video_stats` |
| `video.hotfeed.queue` | `video.*` | 更新首页热门集合 |
| `video.following_inbox.queue` | `video.*` | 发布/删除视频后维护关注流 |
| `user.follow.queue` | `user.*` | 关注/取关后维护关系缓存和关注收件箱 |

主要事件：

- `video.published`
- `video.deleted`
- `video.liked`
- `video.unliked`
- `video.favorited`
- `video.unfavorited`
- `video.commented`
- `video.comment_deleted`
- `user.followed`
- `user.unfollowed`

## 后台任务

任务调度由 `internal/jobs` 管理，启动时根据 `jobs.enabled` 自动运行：

| 任务 | 默认间隔 | 作用 |
| --- | --- | --- |
| `video_stats_reconcile` | `24h` | 扫描可见视频，重新统计点赞/评论/收藏，刷新统计缓存 |
| `hot_feed_dirty_refresh` | `1m` | 批量处理热门脏集合，增量更新首页热门 |
| `hot_feed_rebuild` | `1h` | 全量重建视频统计、首页热门、视频基础缓存、用户摘要缓存 |

任务通过 Redis 分布式锁避免多实例重复执行，锁 TTL 默认 `30m`。

## 本地调试示例

注册并拿 token：

```powershell
$register = Invoke-RestMethod `
  -Method Post `
  -Uri http://localhost:18080/api/v1/auth/register `
  -ContentType "application/json" `
  -Body '{"username":"author001","password":"1234567"}'

$token = $register.data.access_token
```

查询当前用户：

```powershell
Invoke-RestMethod `
  -Uri http://localhost:18080/api/v1/auth/me `
  -Headers @{ Authorization = "Bearer $token" }
```

发布视频：

```powershell
curl.exe -X POST "http://localhost:18080/api/v1/videos" `
  -H "Authorization: Bearer $token" `
  -F "title=hello feed" `
  -F "video=@D:\path\to\sample.mp4" `
  -F "cover=@D:\path\to\cover.png"
```

读取首页：

```powershell
Invoke-RestMethod "http://localhost:18080/api/v1/feed/home?limit=10"
```

## 测试

运行单元测试：

```powershell
cd D:\Feed\backend
go test ./...
```

当前测试覆盖了：

- 热度分计算
- 路由注册
- 关注流缓存部分 Lua/Redis 行为
- 视频统计缓存加载
- 热门评论缓存分页切片

## 压测

压测脚本位于 `backend/scripts/k6`。生成压测数据前，需要 `backend/storage/videos` 下至少有一个 `.mp4` 样例文件，`backend/storage/covers` 下至少有一个 `.png`、`.jpg`、`.jpeg` 或 `.webp` 样例文件。

Ubuntu / Linux 生成种子数据：

```bash
cd ~/Feed/backend
go run ./cmd/seed -base-url http://localhost:18080
```

Windows PowerShell 也可以继续使用原脚本：

```powershell
cd D:\Feed\backend
powershell -ExecutionPolicy Bypass -File .\scripts\loadtest\seed.ps1 -BaseUrl http://localhost:18080
```

脚本会注册作者和观看用户、发布视频、建立关注关系、写入点赞/收藏/评论，并生成：

```text
backend/scripts/loadtest/seed-output.json
```

Ubuntu / Linux 冒烟测试：

```bash
export SEED_DATA_PATH="$PWD/scripts/loadtest/seed-output.json"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/smoke.js
```

Windows PowerShell 冒烟测试：

```powershell
$env:SEED_DATA_PATH="D:\Feed\backend\scripts\loadtest\seed-output.json"
k6 run --system-tags status,method,name,scenario,expected_response .\scripts\k6\smoke.js
```

读混合测试，默认流量约为 15% 匿名首页、70% 登录首页、15% 关注流：

```bash
export SEED_DATA_PATH="$PWD/scripts/loadtest/seed-output.json"
export PAUSE_SECONDS="0"
export READ_VUS="50"
export READ_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/read_mix.js
```

```powershell
$env:SEED_DATA_PATH="D:\Feed\backend\scripts\loadtest\seed-output.json"
$env:PAUSE_SECONDS="0"
$env:READ_VUS="50"
$env:READ_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response .\scripts\k6\read_mix.js
```

写混合测试，默认流量约为 80% 点赞/取消点赞、10% 收藏/取消收藏、10% 评论创建/删除：

```bash
export SEED_DATA_PATH="$PWD/scripts/loadtest/seed-output.json"
export PAUSE_SECONDS="0"
export WRITE_VUS="100"
export WRITE_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/write_mix.js
```

```powershell
$env:SEED_DATA_PATH="D:\Feed\backend\scripts\loadtest\seed-output.json"
$env:PAUSE_SECONDS="0"
$env:WRITE_VUS="100"
$env:WRITE_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response .\scripts\k6\write_mix.js
```

读写混合 soak 测试，默认 80% 读、20% 写：

```bash
export SEED_DATA_PATH="$PWD/scripts/loadtest/seed-output.json"
export PAUSE_SECONDS="0"
export SOAK_VUS="150"
export SOAK_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/soak.js
```

```powershell
$env:SEED_DATA_PATH="D:\Feed\backend\scripts\loadtest\seed-output.json"
$env:PAUSE_SECONDS="0"
$env:SOAK_VUS="150"
$env:SOAK_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response .\scripts\k6\soak.js
```

## 开发注意事项

- `backend/configs/config.yaml` 是本地配置文件，不应提交。
- `backend/storage` 下的真实上传文件不应提交。
- 当前没有手写 SQL migration，表结构由 `internal/model/migrate.go` 的 AutoMigrate 生成。
- 如果将 Go 服务放到容器内运行，需要把 MySQL、Redis、RabbitMQ host 改成 Compose 服务名，例如 `mysql`、`redis:6379`、`rabbitmq:5672`，并为 app 服务补充 HTTP 端口映射。
- 发布视频和删除视频会在主数据变更后发 MQ 事件；RabbitMQ 不可用时相关接口可能返回 `5001`。
- 高频点赞/收藏优先写 Redis 与 Redis Stream，再由后台消费者落库，因此读取统计会依赖缓存和定时对账保证最终一致。
- CORS 默认只允许 `http://localhost:3000`，前端端口变化时需要调整 `cors.allow_origins`。

## License

MIT License。详见 [LICENSE](./LICENSE)。
