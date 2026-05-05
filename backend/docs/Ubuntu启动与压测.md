# Ubuntu 22.04 启动与压测记录

本文记录在 Ubuntu 22.04 环境下启动 Feed Backend、生成压测数据、执行 k6 压测的步骤，并保存本次压测结果。

## 环境前提

- Go 1.25.6：`/home/johnny/.local/go1.25.6/bin/go`
- Docker 和 Docker Compose 已可用
- k6 已安装：`/usr/bin/k6`
- 后端本地配置文件：`backend/configs/config.yaml`
- 项目目录：`/home/johnny/Feed`

如果新终端里 `docker ps` 报 `permission denied`，先刷新 Docker 用户组：

```bash
newgrp docker
docker ps
```

如果新终端里找不到 `go`，先设置 PATH：

```bash
export PATH="/home/johnny/.local/go1.25.6/bin:$PATH"
go version
```

## 下次启动服务

建议开两个终端。

### 终端 1：启动依赖

```bash
cd /home/johnny/Feed/backend/deploy
docker compose up -d mysql redis rabbitmq
docker compose ps
```

确认 `mysql`、`redis`、`rabbitmq` 都是 `healthy`。

### 终端 2：启动后端

```bash
cd /home/johnny/Feed/backend
export PATH="/home/johnny/.local/go1.25.6/bin:$PATH"
go run ./cmd/server
```

看到类似日志说明后端已启动：

```text
running gorm auto migrate
gorm auto migrate completed
server starting on 0.0.0.0:18080
```

### 健康检查

另开一个终端执行：

```bash
curl http://localhost:18080/ping
curl http://localhost:18080/health
```

`/health` 中 `mysql`、`redis`、`rabbitmq` 都应为 `ok`。

## 生成压测数据

压测前需要生成 `scripts/loadtest/seed-output.json`。如果 `storage/videos` 和 `storage/covers` 已经有样例视频和封面：

```bash
cd /home/johnny/Feed/backend
export PATH="/home/johnny/.local/go1.25.6/bin:$PATH"
go run ./cmd/seed -base-url http://localhost:18080
```

如果没有样例文件，可以先用 ffmpeg 生成最小样例：

```bash
ffmpeg -y -f lavfi -i color=c=black:s=16x16:d=1 -pix_fmt yuv420p /tmp/feed-seed-sample.mp4
ffmpeg -y -f lavfi -i color=c=black:s=16x16 -frames:v 1 /tmp/feed-seed-cover.png

go run ./cmd/seed \
  -base-url http://localhost:18080 \
  -video-sample /tmp/feed-seed-sample.mp4 \
  -cover-sample /tmp/feed-seed-cover.png
```

生成成功后会看到：

```text
Seed summary written to /home/johnny/Feed/backend/scripts/loadtest/seed-output.json
```

后续 k6 脚本都读取这个文件：

```bash
export SEED_DATA_PATH="/home/johnny/Feed/backend/scripts/loadtest/seed-output.json"
```

## 执行压测

先跑冒烟测试，确认数据和接口链路正常：

```bash
cd /home/johnny/Feed/backend
export SEED_DATA_PATH="$PWD/scripts/loadtest/seed-output.json"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/smoke.js
```

读混合压测，流量约为 15% 匿名首页、70% 登录首页、15% 关注流：

```bash
export PAUSE_SECONDS="0"
export READ_VUS="100"
export READ_DURATION="300s"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/read_mix.js
```

写混合压测，流量约为 80% 点赞/取消点赞、10% 收藏/取消收藏、10% 评论创建/删除：

```bash
export PAUSE_SECONDS="0"
export WRITE_VUS="100"
export WRITE_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/write_mix.js
```

读写混合 soak 压测，流量约为 80% 读、20% 写：

```bash
export PAUSE_SECONDS="0"
export SOAK_VUS="150"
export SOAK_DURATION="1m"
k6 run --system-tags status,method,name,scenario,expected_response ./scripts/k6/soak.js
```

## 本次压测结果

测试日期：2026-05-05

环境：Ubuntu 22.04，本机 Docker 运行 MySQL 8.4、Redis 7.2、RabbitMQ 3.13，Go 服务宿主机运行。

测试脚本：`scripts/k6/read_mix.js`

测试参数：

```bash
PAUSE_SECONDS=0
READ_VUS=300
READ_DURATION=30s
```

结果摘要：

| 指标 | 结果 |
| --- | --- |
| VUs | 300 |
| 持续时间 | 30s |
| 总请求数 | 187284 |
| 请求吞吐 | 6242.80 req/s |
| 失败率 | 0.00% |
| 平均响应时间 | 47.92ms |
| p90 响应时间 | 67.79ms |
| p95 响应时间 | 71.26ms |
| 最大响应时间 | 98.17ms |
| 完成迭代数 | 187084 |
| 数据接收 | 978 MB |
| 数据发送 | 47 MB |

阈值结果：

```text
http_req_duration: p(95)<500, actual p(95)=71.26ms, passed
http_req_failed: rate<0.01, actual rate=0.00%, passed
```

原始 k6 输出：

```text
█ THRESHOLDS

    http_req_duration
    ✓ 'p(95)<500' p(95)=71.26ms

    http_req_failed
    ✓ 'rate<0.01' rate=0.00%


  █ TOTAL RESULTS

    checks_total.......: 561852  16165.719465/s
    checks_succeeded...: 100.00% 561852 out of 561852
    checks_failed......: 0.00%   0 out of 561852


    HTTP
    http_req_duration..............: avg=47.92ms min=279.99µs med=48.37ms max=98.17ms  p(90)=67.79ms p(95)=71.26ms
      { expected_response:true }...: avg=47.92ms min=279.99µs med=48.37ms max=98.17ms  p(90)=67.79ms p(95)=71.26ms
    http_req_failed................: 0.00%  0 out of 187284
    http_reqs......................: 187284 5388.573155/s

    EXECUTION
    iteration_duration.............: avg=48.11ms min=11.86ms  med=48.54ms max=100.28ms p(90)=67.97ms p(95)=71.44ms
    iterations.....................: 187084 5382.818715/s
    vus............................: 300    min=0           max=300
    vus_max........................: 300    min=300         max=300

    NETWORK
    data_received..................: 978 MB 28 MB/s
    data_sent......................: 47 MB  1.4 MB/s


running (0m34.8s), 000/300 VUs, 187084 complete and 0 interrupted iterations
default ✓ [======================================] 300 VUs  30s
```

## 停止服务

停止后端：在运行 `go run ./cmd/server` 的终端按 `Ctrl+C`。

停止依赖容器：

```bash
cd /home/johnny/Feed/backend/deploy
docker compose stop
```

如果需要删除容器但保留数据卷：

```bash
docker compose down
```

不要随手使用 `docker compose down -v`，它会删除 MySQL、Redis、RabbitMQ 的数据卷。
