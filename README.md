# PDFText

一个用 Go 实现、可编译为单个可执行文件的 PDF 文本提取服务，同时支持 WebUI、REST API、MCP 与 Docker 部署。

## 特性

- 单文件应用：HTML、CSS、JavaScript 和 OpenAPI 定义通过 `go:embed` 编入二进制
- WebUI：拖拽上传、页码范围、版面保留、复制和下载
- REST API：`POST /api/v1/extract`
- MCP：Streamable HTTP 与 stdio 两种传输
- 安全限制：文件/输出大小、处理超时、路径隔离、Host/Origin 校验
- Docker：多阶段构建、非 root、只读根文件系统、CPU/内存限制

底层使用 Poppler 的 `pdftotext`，输出统一为 UTF-8。

## 与 `thr3a/pdftotext` 的关系

[`thr3a/pdftotext`](https://hub.docker.com/r/thr3a/pdftotext) 是 Docker Hub 镜像，不是源码项目。它的核心入口是：

```text
/usr/bin/pdftotext -layout - -
```

也就是从标准输入读取 PDF，再向标准输出写文本。本项目保留这个行为，但在其外层增加了 Go Web 服务、MCP、资源限制和安全控制；不挂载 Docker Socket，也不会在容器中再次启动容器。

“单文件”指 PDFText 应用自身只需一个 Go 可执行文件。为保持 Poppler 的提取质量，运行环境仍需安装 `pdftotext`；项目 Docker 镜像已经包含它。

## Docker 快速开始

```bash
docker compose up -d --build
```

启动后：

- WebUI：<http://localhost:8000>
- OpenAPI：<http://localhost:8000/openapi.json>
- MCP Streamable HTTP：`http://localhost:8000/mcp`
- 存活检查：<http://localhost:8000/healthz>
- 就绪检查：<http://localhost:8000/readyz>

也可以直接拉取 GitHub Container Registry 中由 GitHub Actions 自动构建的镜像：

```bash
docker pull ghcr.io/springsunx/pdftext:latest
docker run --rm -p 8000:8000 ghcr.io/springsunx/pdftext:latest
```

停止：

```bash
docker compose down
```

## 编译为单文件

需要 Go 1.26.5 或更高的安全修复版本。`go.mod` 中的 `toolchain` 指令会在支持自动工具链的 Go 环境中选择该版本。

当前系统：

```bash
CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags="-s -w -X main.version=0.1.0" \
  -o pdftext .
```

交叉编译 Linux amd64：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -ldflags="-s -w -X main.version=0.1.0" \
  -o pdftext-linux-amd64 .
```

交叉编译 Windows amd64：

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
  -trimpath -ldflags="-s -w -X main.version=0.1.0" \
  -o pdftext-windows-amd64.exe .
```

二进制运行模式：

```bash
# WebUI + REST + MCP Streamable HTTP
./pdftext serve

# MCP stdio，供本地桌面客户端启动
./pdftext mcp

# 容器/进程健康检查
./pdftext healthcheck
```

不传参数时默认使用 `serve`。

## REST API

上传并提取全文：

```bash
curl -X POST http://localhost:8000/api/v1/extract \
  -F "file=@document.pdf" \
  -F "layout=true" \
  -F "page_breaks=true"
```

只提取第 2 到第 5 页：

```bash
curl -X POST http://localhost:8000/api/v1/extract \
  -F "file=@document.pdf" \
  -F "first_page=2" \
  -F "last_page=5"
```

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `file` | PDF 文件 | 必填 | 待处理 PDF |
| `layout` | boolean | `true` | 尽量保留原文列与间距 |
| `page_breaks` | boolean | `true` | 保留页面间的换页符 |
| `first_page` | integer | 空 | 起始页，从 1 开始 |
| `last_page` | integer | 空 | 结束页，从 1 开始 |
| `password` | string | 空 | 加密 PDF 的用户密码 |

## MCP 接入

### Streamable HTTP

让 MCP 客户端连接：

```text
http://localhost:8000/mcp
```

使用官方 Go MCP SDK v1.7.0，HTTP 端点采用 stateless + JSON response 模式，并兼容 SDK 支持的旧协议版本。

提供两个工具：

- `extract_pdf_base64`：接收 Base64 PDF，默认上限 2 MB
- `extract_pdf_file`：读取服务端允许目录中的 PDF，适合较大文件和批处理

### 只读目录工具

Compose 默认把 `data/input` 只读挂载到容器内 `/data/input`：

1. 将 `report.pdf` 放到 `data/input/report.pdf`。
2. 调用 `extract_pdf_file`。
3. 参数 `relative_path` 填写 `report.pdf`。

工具拒绝绝对路径、目录穿越、越界软链接和非 `.pdf` 文件。

### stdio

本地 MCP 客户端可以直接启动：

```text
pdftext mcp
```

stdio 模式只通过标准输入输出传输 MCP 消息，日志写入标准错误。

## 配置

复制 `.env.example` 为 `.env` 后可调整 Compose 配置：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PDFTEXT_IMAGE` | `ghcr.io/springsunx/pdftext:latest` | Compose 使用或构建的镜像名 |
| `PDFTEXT_PORT` | `8000` | Compose 暴露的宿主机端口 |
| `PDFTEXT_ADDR` | `:8000` | Go HTTP 监听地址 |
| `PDFTEXT_MAX_FILE_SIZE_MB` | `50` | Web、REST 和目录型 MCP 的 PDF 上限 |
| `PDFTEXT_MAX_OUTPUT_SIZE_MB` | `100` | 提取文本上限 |
| `PDFTEXT_TIMEOUT_SECONDS` | `120` | 单次 Poppler 处理超时 |
| `PDFTEXT_MCP_MAX_FILE_SIZE_MB` | `2` | Base64 MCP 工具的 PDF 上限 |
| `PDFTEXT_MCP_ALLOWED_ROOT` | `/data/input` | MCP 文件工具允许读取的根目录 |
| `PDFTEXT_MCP_ALLOWED_HOSTS` | 本机地址 | MCP HTTP 允许的 Host，逗号分隔 |
| `PDFTEXT_MCP_ALLOWED_ORIGINS` | 本机地址 | MCP HTTP 允许的 Origin，逗号分隔 |
| `PDFTEXT_BINARY` | `pdftotext` | Poppler 程序名或完整路径 |

通过域名或反向代理公开 MCP 时，必须把真实域名加入 `PDFTEXT_MCP_ALLOWED_HOSTS`。浏览器型 MCP 客户端还需要配置 `PDFTEXT_MCP_ALLOWED_ORIGINS`。

## 本地开发与测试

安装 Poppler 后运行：

```bash
go test ./...
go run . serve
```

Debian/Ubuntu：

```bash
sudo apt-get install poppler-utils
```

Windows 可安装 Poppler，并通过 `PDFTEXT_BINARY` 指向 `pdftotext.exe`。如果只需要部署，直接使用 Docker 更简单。

## 安全边界

- 上传使用流式大小限制，避免先完整接收超大请求。
- Poppler 子进程有超时，输出写入受限缓冲区，不会无限占用内存。
- 不向客户端返回服务器路径或原始 Poppler stderr。
- MCP HTTP 有请求体上限，并校验 Host 与 Origin。
- MCP 文件读取根目录会解析软链接，防止通过目录穿越逃逸。
- Compose 默认使用非 root、只读根文件系统、移除 Linux capabilities。
- 公网部署仍建议在反向代理层增加 HTTPS、身份认证和限流。

## 自动构建镜像

GitHub Actions 会先运行 Go 测试和静态检查，再使用 Buildx 构建 `linux/amd64` 与 `linux/arm64` 镜像并推送到：

```text
ghcr.io/springsunx/pdftext
```

- 推送默认分支：生成 `latest`、分支名和提交 SHA 标签
- 推送 `v*` 版本标签：生成语义化版本标签
- Pull Request：只构建验证，不推送镜像
- 支持在 Actions 页面手动触发

## 已知限制

- Poppler 只能提取 PDF 已有的文本层；纯扫描件需要 OCR，本项目当前不包含 OCR。
- 复杂表格、字体映射异常和特殊排版的结果取决于 Poppler。
- Base64 会让请求体增大约三分之一，大文件应使用 MCP 只读目录工具。

## 项目结构

```text
main.go            # 运行模式与优雅关闭
api.go             # WebUI、REST 和嵌入资源
extractor.go       # Poppler 调用与资源限制
mcp.go             # MCP 工具、HTTP 与 stdio
config.go          # 环境变量配置
src/pdftext/       # 编入二进制的 WebUI/OpenAPI 资源
*_test.go          # 单元和协议集成测试
Dockerfile
compose.yaml
```

## License

MIT
