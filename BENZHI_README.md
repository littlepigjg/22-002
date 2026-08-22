# BENZHI_README · 设备固件升级管理服务（评测专用）

> 项目代号：`firmware-upgrade`（简称 `fu`）
> 模式：0→1 自研完整项目 → 给出可注入缺陷的位置（BUG_CATALOG.md，30 条）
> 技术栈：纯 Go 标准库（`net/http`、`encoding/json`、`sync/atomic`、`hash/*`），不引入任何第三方依赖。
> 端口：**8080**（不可配置的默认端口；同时支持通过 `APP_PORT` 覆盖）

---

## 1. 项目总览

本服务用于设备 OTA 固件升级的全流程管理，包含：

- 设备型号（DeviceModel）与固件版本（Firmware）的元数据管理。
- 设备注册（Device Register）+ 心跳（Heartbeat）+ 在线状态。
- 升级任务（UpgradeTask）创建，支持全量、灰度比例、设备名单三种策略。
- 设备端主动轮询（Poll）与服务端下发升级命令，下载/升级进度上报。
- 升级历史、统计概览、版本分布、每日统计、任务成功率。

分层：

```
cmd/server/main.go          // 入口：HTTP 服务 + 后台 worker + 优雅关闭
internal/config             // 环境变量驱动的配置
internal/model              // 实体、DTO、错误码
internal/store              // 内存存储（泛型 TTL 缓存、互斥锁）
internal/service            // 业务逻辑（型号 / 固件 / 设备 / 任务 / 灰度 / 轮询 / 进度 / 历史 / 统计 / 文件操作）
internal/handler            // REST 路由与中间件（AccessLog、Recovery、Trace、CORS、UserContext、RateLimit）
internal/middleware         // 额外中间件：用户注入、速率限制
internal/dto                // DTO 辅助：分页、指针映射
internal/seed               // 启动时演示数据注入（SEED_DATA=1）
pkg/*                       // 通用工具包：logger/response/timeutil/idgen/hashutil/md5util
                            // fileutil/validate/strutil/randutil/cache/safemap/retry/pool/httpx
```

规模统计（最新一次）：

- Go 文件数（不含测试）：**53**
- 代码行数（不含测试）：**9272**
- BUG 候选：**30**（详见 BUG_CATALOG.md）

---

## 2. 快速开始

### 2.1 本地直接运行

```bash
# 1) 构建 & 运行
go build -o bin/server ./cmd/server
SEED_DATA=1 LOG_LEVEL=debug ./bin/server

# 2) 验证接口
curl -s http://127.0.0.1:8080/health/live  | python3 -m json.tool
curl -s http://127.0.0.1:8080/api/v1/stats/overview | python3 -m json.tool
# 打开 Web 控制台
xdg-open http://127.0.0.1:8080/   # 或 macOS 下 open
```

### 2.2 Docker 构建与运行（推荐评测使用）

```bash
# 构建镜像（含 --load 会自动 load 到本地 docker images）
./build_benzhi_docker.sh --load --tag firmware-upgrade-benzhi:test

# 一次性启动
docker run --rm -p 8080:8080 -e SEED_DATA=1 firmware-upgrade-benzhi:test

# 后台运行 + 持久化
mkdir -p ./data && \
docker run -d --name fu-benzhi \
  -p 8080:8080 \
  -v "$PWD/data":/app/data \
  -e SEED_DATA=1 \
  -e LOG_LEVEL=debug \
  --health-cmd="curl -fsS http://127.0.0.1:8080/health/live || exit 1" \
  firmware-upgrade-benzhi:test

# 验证
curl http://127.0.0.1:8080/health/live
curl http://127.0.0.1:8080/api/v1/stats/overview
```

构建脚本支持国内代理：`./build_benzhi_docker.sh --proxy cn --load`。

---

## 3. 接口一览（RESTful）

### 3.1 公共

| Method | Path | 说明 |
|--------|------|------|
| GET | `/` | 嵌入的 Web 控制台（单文件前端） |
| GET | `/health/live` | 存活检测 |
| GET | `/health/ready` | 就绪检测 |
| GET | `/health/metrics` | 构建版本、Go 指标 |

### 3.2 设备型号

| Method | Path | 说明 |
|--------|------|------|
| POST | `/api/v1/models` | 创建设备型号 |
| PUT | `/api/v1/models/{id}` | 更新型号 |
| DELETE | `/api/v1/models/{id}` | 删除型号 |
| GET | `/api/v1/models/{id}` | 型号详情 |
| GET | `/api/v1/models` | 分页列表（支持 keyword/enabled） |

### 3.3 固件版本

| Method | Path | 说明 |
|--------|------|------|
| POST | `/api/v1/firmwares` | 创建固件元数据（不含文件） |
| PUT | `/api/v1/firmwares/{id}/status` | 发布 / 撤销：`{"action":"publish"|"revoke"}` |
| DELETE | `/api/v1/firmwares/{id}` | 删除固件 |
| GET | `/api/v1/firmwares/{id}` | 固件详情 |
| GET | `/api/v1/firmwares` | 分页列表（支持 model_id/status/keyword） |
| POST | `/api/v1/firmwares/upload` | multipart 上传固件文件，返回文件 MD5/大小/路径 |
| GET | `/api/v1/firmwares/{id}/download` | 下载固件文件 |

### 3.4 设备管理

| Method | Path | 说明 |
|--------|------|------|
| POST | `/api/v1/devices/register` | 注册设备 |
| POST | `/api/v1/devices/heartbeat` | 心跳（X-Device-Id） |
| DELETE | `/api/v1/devices/{id}` | 删除设备 |
| GET | `/api/v1/devices/{id}` | 设备详情 |
| GET | `/api/v1/devices` | 分页列表（支持 model_id/status/group/keyword） |

### 3.5 升级任务

| Method | Path | 说明 |
|--------|------|------|
| POST | `/api/v1/tasks` | 创建任务（灰度比例 / 设备名单 / 全量） |
| GET | `/api/v1/tasks/{id}` | 任务详情（含执行记录） |
| GET | `/api/v1/tasks` | 分页列表 |
| POST | `/api/v1/tasks/{id}/action` | 启动 / 暂停 / 继续 / 取消：`{"action":"start|pause|resume|cancel"}` |
| DELETE | `/api/v1/tasks/{id}` | 删除任务（running 不可删） |

### 3.6 设备端

| Method | Path | 说明 |
|--------|------|------|
| POST | `/api/v1/devices/poll` | 设备轮询（body: `device_id, current_version, status`） |
| POST | `/api/v1/devices/progress` | 设备上报进度 / 结果：`device_id, task_id, progress[0-100], status, error_msg` |

### 3.7 历史 + 统计

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/v1/devices/{id}/history` | 单个设备最近升级历史 |
| GET | `/api/v1/stats/overview` | 总览：设备数 / 固件数 / 任务数 / 成功率 / 在线分布 / 版本分布 |
| GET | `/api/v1/stats/daily` | 最近 N 天每日升级统计 |

所有业务接口统一响应格式：

```json
{"code":0, "message":"ok", "request_id":"...", "data":{...}}
```

分页列表：

```json
{"list":[], "page":{"page_num":1,"page_size":20,"total":0}}
```

---

## 4. 配置（环境变量）

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `APP_ENV` | `production` | `dev`/`production` |
| `APP_PORT` | `8080` | 监听端口 |
| `LOG_LEVEL` | `info` | `debug/info/warn/error` |
| `DATA_DIR` | `./data` | 运行数据根目录 |
| `FIRMWARE_DIR` | `$DATA_DIR/firmwares` | 固件文件存储目录 |
| `MAX_UPLOAD_MB` | `100` | 单固件上传最大 MB |
| `HEARTBEAT_TTL` | `120` | 心跳 TTL（秒） |
| `PROGRESS_TIMEOUT` | `300` | 单次升级进度超时（秒） |
| `WORKER_TICK` | `10` | 后台 worker 周期（秒） |
| `SHUTDOWN_TIMEOUT` | `30` | 优雅关闭宽限（秒） |
| `SEED_DATA` | `0` | `1` 启动时注入演示数据（型号/固件/设备） |

---

## 5. 文件清单

```
├── BUG_CATALOG.md             30 个跨文件缺陷候选清单（仅说明，不实际植入）
├── benzhi.Dockerfile          多阶段构建 Dockerfile（Go builder + alpine runner）
├── build_benzhi_docker.sh     构建脚本（--tag/--proxy/--load/--save 等）
├── .dockerignore              构建上下文忽略项
├── go.mod                     module firmware-upgrade, Go 1.22
├── BENZHI_README.md           本文件
├── cmd/server/main.go         入口（HTTP + workers + graceful shutdown）
├── internal/{config,model,store,service,handler,middleware,dto,seed}
└── pkg/{logger,response,timeutil,idgen,fileutil,hashutil,md5util,
         validate,strutil,randutil,cache,safemap,retry,pool,httpx}
```

---

## 6. 质量校验

```bash
cd /home/admin/code/22/22-002
go build ./...     # 无输出表示编译通过
go vet   ./...     # 无输出表示静态检查通过

# 可选：启动后用 curl 验证
SEED_DATA=1 go run ./cmd/server >/tmp/fu.log 2>&1 &
sleep 2
curl -s http://127.0.0.1:8080/health/live | head -c 200
curl -s http://127.0.0.1:8080/api/v1/stats/overview | head -c 300
kill %1
```

---

## 7. 可注入缺陷的位置

见本目录下 [`BUG_CATALOG.md`](./BUG_CATALOG.md)，列出 **30 条** 跨文件的运行时缺陷（并发竞争、nil、切片共享、错误包装、ctx 透传、defer、路径拼接等）。

每条缺陷包含：
- 唯一 `bug_id` / 分类（concurrency/nil/slice/error/context/defer/other）
- 植入位置（跨 2–3 个文件、带方法名与行号锚点目标）
- 预期表现
- 触发方式
- 难度星级 ★~★★★★★

> **类别 & 文件占比合规性**：
> - 类别最高：concurrency 8 条 = 26.7% ≤ 30%
> - 单文件最高：progress_service.go / task_service.go / device_store.go 各 5 条 = 16.7% ≤ 30%

---

## 8. 约束合规性对照

| 约束 | 要求 | 满足情况 |
|------|------|----------|
| 编译通过 | `go build ./...` 0 错误 | ✅ |
| 静态检查 | `go vet ./...` 0 错误 | ✅ |
| 纯 Go 标准库 | 零第三方 import | ✅（go.mod 仅 `module firmware-upgrade` 与 `go 1.22`） |
| 监听端口 | 8080 | ✅ 默认与 Docker EXPOSE |
| 大型规模 | Go 文件 ≥50，行 ≥3000，缺陷 ≥30 | ✅ 53 文件 / 9272 行 / 30 条缺陷 |
| 单文件缺陷占比 | 任一文件 ≤ 30% | ✅ 最高 16.7% |
| 类别占比 | 任一类 ≤ 30% | ✅ 最高 26.7% |
| 仅注入说明 | BUG_CATALOG 只说明，不实际修改源码 | ✅ |
| 打包 Docker | Dockerfile + 构建脚本 + README + .dockerignore | ✅ |

---

## 9. 路线图（可选后续）

- [ ] 引入 SQLite / Postgres 持久化存储
- [ ] 设备端 MQTT / CoAP 通道
- [ ] 固件差分升级（bsdiff）
- [ ] Web 控制台真正集成前端构建产物
- [ ] 单元测试 + 端到端自动化用例
