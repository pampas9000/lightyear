# 对外 API 接入指南

本页面向用程序调用 Lightyear 的开发者：从拿到 API Key，到上传图片、提交转换任务、等待完成、下载产物，每一步给出可以直接运行的 `curl` 命令，最后是一个完整脚本。逐字段的定义与全部错误码见 [HTTP API 契约 §对外 API /v1](/contracts/http-api#对外-api-v1)。

API 只做异步处理：提交任务后立即返回，处理在后台进行，客户端轮询任务状态。目前没有 webhook 回调。

给 Agent 用时，可以不写脚本，直接把 `/mcp` 配置成 MCP 服务器，见[通过 MCP 接入](#通过-mcp-接入)。

## 准备

**API 地址。** `/v1` 由 API 服务直接提供，本地开发时是 `http://localhost:8080`。下文用环境变量 `LIGHTYEAR_API` 表示它。

**API Key。** 在 Web 控制台右上角的账户菜单打开「API Keys」，填写名称、选择权限后创建。完整的 key（`ly_` 开头）只在创建后显示这一次，服务端只保存它的哈希，丢失后只能吊销再新建。

| 权限（scope） | 允许 |
| :--- | :--- |
| `tasks:read` | 查询任务列表与单个任务（含下载链接） |
| `tasks:write` | 上传文件、提交任务、取消、重试、删除任务与文件 |

两个权限互不包含：只做提交的程序也需要 `tasks:read` 才能轮询结果。在控制台吊销后，该 key 的请求立即被拒绝。

```bash
export LIGHTYEAR_API=http://localhost:8080
export LIGHTYEAR_API_KEY=ly_…        # 不要写进代码仓库
```

每个请求都带上：

```http
Authorization: Bearer ly_…
```

下文的命令还用到 `jq` 解析 JSON，以及 `shasum`（macOS 自带；Linux 上也可以用 `sha256sum`）。

## 响应格式

所有响应（`304` 除外）都是同一个信封，五个字段始终出现，字段一律 `snake_case`：

```json
{ "success": true, "code": "ok", "message": "Task accepted.", "data": { … }, "details": null }
```

成功时 `code` 总是 `ok`。失败时 `success` 为 `false`，`code` 是稳定的错误码，`details` 可能补充出错的字段或原因（没有时为 `null`）：

```json
{ "success": false, "code": "error.param.invalid", "message": "…", "data": null,
  "details": { "field": "items", "reason": "file not found or not uploaded" } }
```

程序应该按 HTTP 状态码和 `code` 判断，不要解析 `message`。请求体按严格 JSON 解析：拼错或多余的字段返回 `400 error.body.invalid`，而不是被静默忽略。

## 第 1 步：上传源图

文件不经过 API 服务，而是由客户端直接上传到对象存储：先向 API 申请上传地址，上传完成后再通知 API。

### 申请上传

```bash
FILE=photo.jpg
curl -sS -X POST "$LIGHTYEAR_API/v1/uploads" \
  -H "Authorization: Bearer $LIGHTYEAR_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "$(jq -n \
        --arg name "$(basename "$FILE")" \
        --argjson size "$(wc -c < "$FILE" | tr -d ' ')" \
        --arg sha256 "$(shasum -a 256 "$FILE" | cut -d ' ' -f 1)" \
        '{filename: $name, size: $size, content_type: "image/jpeg", sha256: $sha256}')"
```

| 字段 | 说明 |
| :--- | :--- |
| `filename` | 必填，决定产物的文件名（`photo.jpg` 转 AVIF 得到 `photo.avif`） |
| `size` | 必填，字节数 |
| `content_type` | 可选，默认 `application/octet-stream` |
| `sha256` | 可选但建议提供，十六进制。提供后存储会校验上传内容；已经上传过同样内容的文件时，可以免去再次上传 |

响应的 `data.file.id` 是之后提交任务要用的文件 ID，`data.method` 决定下一步：

| `method` | 状态码 | 下一步 |
| :--- | :--- | :--- |
| `none` | `200` | 你已经上传过相同 `sha256` 的文件，`data.file` 就是它，直接去[提交任务](#第-2-步-提交任务) |
| `put` | `201` | 文件 ≤ 10 MB：把文件 `PUT` 到 `data.url`，再通知完成 |
| `multipart` | `201` | 文件 > 10 MB：分片上传，见[大文件分片上传](#大文件分片上传) |

### 上传并通知完成

```bash
curl -sS --fail -X PUT --upload-file "$FILE" "$UPLOAD_URL"     # data.url

curl -sS -X POST "$LIGHTYEAR_API/v1/uploads/$FILE_ID/complete" \
  -H "Authorization: Bearer $LIGHTYEAR_API_KEY"
```

- 上传地址是对象存储的预签名 URL，不要给它带 `Authorization` 头。它在 `data.expires_at`（1 小时）后失效，过期了就重新申请上传。
- `complete` 返回的文件状态为 `UPLOADED`，此后才能用于任务。对已完成的文件重复调用 `complete` 是安全的。
- 申请后一直没有完成的上传，24 小时后会被清理。

## 第 2 步：提交任务

一个任务把一组源文件按同一组输出参数转换，每个「源文件 × 输出」是一个 job。输出参数是 [ImageOptions](/contracts/image-options)，只有 `format` 必填。

```bash
curl -sS -i -X POST "$LIGHTYEAR_API/v1/tasks" \
  -H "Authorization: Bearer $LIGHTYEAR_API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: import-2026-11-17-photo" \
  -d '{
        "items": [{ "file_id": "'"$FILE_ID"'" }],
        "options": { "format": "avif", "resize": { "width": 1600 } }
      }'
```

```http
HTTP/1.1 202 Accepted
Location: /v1/tasks/0198…
Retry-After: 2
```

响应体是任务本身，`data.id` 是任务 ID。描述输出有三种写法，必须且只能选一种：

**单个输出**：`options`，如上。

**限定文件大小**：`options` 加 `target.max_bytes`。省略 `quality`（JXL 是 `distance`）时，服务端在该格式内搜索不超过目标大小的最高质量；完成后 job 的 `resolved_options` 是实际采用的参数，`target_met` 表示是否达标。PNG 不支持 `target`。

```json
{ "items": [{ "file_id": "0198…" }],
  "options": { "format": "webp" },
  "target": { "max_bytes": 204800 } }
```

**多个输出**：`outputs`，一次生成多份，`name` 会出现在产物文件名里（`photo.thumb.webp`）。

```json
{ "items": [{ "file_id": "0198…" }, { "file_id": "0198…" }],
  "outputs": [
    { "name": "thumb", "options": { "format": "webp", "resize": { "width": 320 } } },
    { "name": "full",  "options": { "format": "avif" }, "target": { "max_bytes": 512000 } }
  ] }
```

第三种写法是 `workflow_id`，引用在控制台保存的预设。

限制：每个任务最多 100 个源文件、10 个输出、共 200 个 job；文件必须属于你、且已经 `UPLOADED`。同一源文件、同一参数以前处理过时，job 会直接以 `COMPLETED` 返回，不再重复处理。

### 直接用 URL 作为源

图已经在网上时，可以跳过第 1 步，在 `items` 里给 `url`，由 worker 去下载：

```bash
curl -sS -i -X POST "$LIGHTYEAR_API/v1/tasks" \
  -H "Authorization: Bearer $LIGHTYEAR_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
        "items": [{ "url": "https://images.example.com/photos/cat.jpg" }],
        "options": { "format": "webp" }
      }'
```

- 每个 item 只能给 `file_id` 与 `url` 之一，两种可以混在同一个任务里。
- 提交时只检查 URL 格式，下载发生在执行时；下载前 job 的 `source_file.status` 是 `FETCHING`。
- 只能下载公网上的 `http`/`https` 地址，内网与本机地址会以 `url_rejected` 失败；远端超时或 5xx 是 `fetch_failed`，会自动重试，远端 4xx 不会。
- 不会带任何 Cookie 或凭据。**私有存储桶的图请传预签名 URL**（如 S3/R2 的 presigned GET），并让它的有效期覆盖任务排队与执行的时间；过期的链接会以 `fetch_failed`（403）失败。
- 同一个 URL 每次提交都会重新下载和处理，不复用以前的结果。

### 用 `Idempotency-Key` 安全重试

网络超时后，你无法确定任务是否已经创建。带上 `Idempotency-Key` 后可以放心重发同一个请求：

- 相同的 key、相同的请求内容：返回第一次创建的任务，状态码仍是 `202`，并带 `Idempotent-Replayed: true`，不会重复创建。
- 相同的 key、不同的请求内容：`409 error.idempotency.conflict`。
- key 由你生成（最长 255 字节），对整个账户有效、不会过期，所以每批不同的工作要用不同的 key，例如「批次号 + 文件哈希」。
- 不带这个头时，每次请求都会创建新任务。

## 第 3 步：等待完成

```bash
curl -sS -i "$LIGHTYEAR_API/v1/tasks/$TASK_ID" \
  -H "Authorization: Bearer $LIGHTYEAR_API_KEY"
```

任务的 `status`：

| 状态 | 含义 |
| :--- | :--- |
| `PENDING` / `PROCESSING` | 未结束，继续等待 |
| `COMPLETED` | 全部 job 成功 |
| `PARTIALLY_FAILED` | 部分 job 失败 |
| `FAILED` | 全部 job 失败 |
| `CANCELLED` | 已取消 |

轮询时这样做，既不浪费请求也不重复下载同样的内容：

- 任务未结束时响应带 `Retry-After`（秒），按它的间隔再查。
- 记下响应的 `ETag`，下次请求带 `If-None-Match: <ETag>`。任务没有变化时返回 `304`，没有响应体。任何 job 的状态或进度变化都会产生新的 ETag。

每个 job 有自己的 `status` 与 `progress`（0–100）。失败的 job 带 `error_code` 与 `error_message`；`attempts` / `max_attempts` 是已经尝试的次数和上限，服务端会自动重试临时性的失败。

## 第 4 步：下载产物

`GET /v1/tasks/:id` 的响应中，每个已完成 job 的 `output_file` 带下载链接：

```json
"output_file": {
  "id": "0198…", "name": "photo.avif", "status": "UPLOADED", "size": 98400, "mime_type": "image/avif",
  "download_url": "https://…",
  "download_expires_at": "2026-11-17T11:00:00Z"
}
```

```bash
curl -sS --fail -o photo.avif "$DOWNLOAD_URL"
```

- 链接是对象存储的预签名地址，不要带 `Authorization` 头，下载时文件名就是 `name`。
- 链接 1 小时后失效，过期了就重新 `GET` 任务拿新链接。收到 `304` 时，你之前拿到的链接至少还有 30 分钟有效期。
- 只有 `GET /v1/tasks/:id` 带下载链接；任务列表、提交、取消、重试的响应都不带。
- 产物与源文件不会永久保存：默认在上传完成 30 天后删除，到期时间见文件的 `expires_at`（部署方可以改，`0` 表示永久保留）。请在到期前把需要的产物下载到自己的存储。到期后 job 仍是 `COMPLETED`，但 `output_file.status` 变为 `EXPIRED`，不再带 `download_url`；需要时重新提交任务即可生成新的产物。

job 的 `compression` 给出源文件与产物的大小对比（`output_bytes`、`saved_ratio` 等）；`larger_than_source` 为 `true` 时，产物比源文件还大，可以考虑直接用源文件。

## 完整脚本

上传一个文件（≤ 10 MB），转成 AVIF，等待完成并下载产物：

```bash
#!/usr/bin/env bash
# usage: lightyear-convert.sh photo.jpg
set -euo pipefail
: "${LIGHTYEAR_API:?}" "${LIGHTYEAR_API_KEY:?}"

file=$1
name=$(basename "$file")
size=$(wc -c < "$file" | tr -d ' ')
sha=$(shasum -a 256 "$file" | cut -d ' ' -f 1)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

api() { curl -sS --fail-with-body -H "Authorization: Bearer $LIGHTYEAR_API_KEY" "$@"; }

# 1. 上传
init=$(api -X POST "$LIGHTYEAR_API/v1/uploads" -H 'Content-Type: application/json' \
  -d "$(jq -n --arg n "$name" --argjson s "$size" --arg h "$sha" \
        '{filename: $n, size: $s, sha256: $h, content_type: "image/jpeg"}')")
file_id=$(jq -r '.data.file.id' <<<"$init")
case $(jq -r '.data.method' <<<"$init") in
  none) ;;
  put)
    curl -sS --fail -X PUT --upload-file "$file" "$(jq -r '.data.url' <<<"$init")"
    api -X POST "$LIGHTYEAR_API/v1/uploads/$file_id/complete" > /dev/null ;;
  *) echo "文件超过 10 MB，需要分片上传" >&2; exit 1 ;;
esac

# 2. 提交（重复运行同一个文件会拿回同一个任务）
task=$(api -X POST "$LIGHTYEAR_API/v1/tasks" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: avif-$sha" \
  -d "$(jq -n --arg f "$file_id" '{items: [{file_id: $f}], options: {format: "avif"}}')")
task_id=$(jq -r '.data.id' <<<"$task")
echo "task $task_id"

# 3. 轮询：带 If-None-Match，按 Retry-After 等待
etag=
while :; do
  args=(-o "$work/task.json" -D "$work/headers" -w '%{http_code}')
  [ -n "$etag" ] && args+=(-H "If-None-Match: $etag")
  code=$(curl -sS "${args[@]}" -H "Authorization: Bearer $LIGHTYEAR_API_KEY" \
    "$LIGHTYEAR_API/v1/tasks/$task_id")
  header() { grep -i "^$1:" "$work/headers" | cut -d ' ' -f 2- | tr -d '\r'; }
  case $code in
    200)
      etag=$(header etag)
      status=$(jq -r '.data.status' "$work/task.json")
      echo "status $status"
      case $status in COMPLETED|PARTIALLY_FAILED|FAILED|CANCELLED) break ;; esac ;;
    304) ;;
    *) cat "$work/task.json" >&2; exit 1 ;;
  esac
  sleep "$(header retry-after || echo 2)"
done

# 4. 下载成功的产物，打印失败的 job
jq -r '.data.jobs[] | select(.output_file.download_url) | [.output_file.name, .output_file.download_url] | @tsv' \
  "$work/task.json" |
while IFS=$'\t' read -r out url; do
  curl -sS --fail -o "$out" "$url" && echo "saved $out"
done
jq -r '.data.jobs[] | select(.status == "FAILED") | "failed: \(.error_code) \(.error_message)"' "$work/task.json" >&2
```

`--fail-with-body` 需要 curl 7.76 或更新的版本。

## 大文件分片上传

`method` 为 `multipart` 时，响应带 `part_size` 与 `part_urls`：

1. 按 `part_size` 字节切分文件，第 `i + 1` 片 `PUT` 到 `part_urls[i]`，记下每片响应头里的 `ETag`（带引号，原样保留）。
2. 全部上传后调用 `complete`，带上每片的编号与 ETag：

   ```json
   { "parts": [{ "part_number": 1, "etag": "\"3858f6…\"" }, { "part_number": 2, "etag": "\"a1b2c3…\"" }] }
   ```

3. 中断后续传：`GET /v1/uploads/:id/parts` 列出已上传的分片；某片的地址过期时，`POST /v1/uploads/:id/parts/:part_number/url` 重新签名。
4. 放弃上传：`POST /v1/uploads/:id/abort`。

## 取消与重试

- `POST /v1/tasks/:id/cancel`：取消所有未结束的 job，已完成的 job 不受影响。
- `POST /v1/tasks/:id/retry`：不带请求体时重试所有失败的 job，返回 `202`。也可以用 `job_ids` 指定 job，或对单个 job 换一组 `options` / `target` 重做，字段见[契约 §重试](/contracts/http-api#重试)。

## 删除

不再需要的任务和文件可以提前删除，不必等它们过期：

```bash
curl -sS -X DELETE "$LIGHTYEAR_API/v1/tasks/$TASK_ID" \
  -H "Authorization: Bearer $LIGHTYEAR_API_KEY"
# {"data": {"id": "…", "deleted_outputs": ["…"], "retained_outputs": []}, …}

curl -sS -X DELETE "$LIGHTYEAR_API/v1/files/$FILE_ID" \
  -H "Authorization: Bearer $LIGHTYEAR_API_KEY"
```

- 删除不可撤销，需要 `tasks:write`。
- 任务还在处理时返回 `409 error.task.conflict`：先 `POST /v1/tasks/:id/cancel`，再删除。
- 删除任务会删除它的 job 和产物，但不删除源文件。如果另一个任务复用了同一个产物（同一源文件、同样的参数），这个产物会保留下来，列在 `retained_outputs` 里。
- `DELETE /v1/files/:id` 可以删除源文件或产物；文件正在被未完成的任务读取时返回 `409 error.file.conflict`。删除后，引用它的 job 显示 `status: "DELETED"`。

## 错误处理

| 状态码 | 常见原因 | 怎么办 |
| :--- | :--- | :--- |
| `400` | 请求体或参数不合法，`details` 指出字段 | 修正请求，不要原样重试 |
| `401` | 没带 key、key 写错或已吊销 | 检查 `Authorization` 头与 key 状态 |
| `403 error.auth.insufficient_scope` | key 缺少权限，`details.required_scope` 给出所需权限 | 换一个有该权限的 key |
| `403 error.quota.exceeded` | 本月处理量用完，或存储已满；`details` 给出 `quota`、`limit`、`used`、`reserved`、`requested` | 等下个月（UTC），或删除不再需要的文件；用 `GET /v1/usage` 查看用量 |
| `404` | 任务或文件不存在，或不属于这个 key 的账户 | 检查 ID |
| `409` | `Idempotency-Key` 已用于不同的请求；重试了未结束的 job；删除仍在处理的任务或文件；重试时源文件已过期（`error.file.gone`） | 按 `code` 处理，见[契约 §错误码](/contracts/http-api#错误码) |
| `429 error.rate_limit.exceeded` | 写请求太频繁（默认每个用户每分钟 60 次，可连续 30 次） | 等 `Retry-After` 秒后原样重试 |
| `5xx`、超时、连接失败 | 服务端或网络问题 | 等待后重试；提交任务时带 `Idempotency-Key`，重试不会产生重复任务 |

## 其他端点

- `GET /v1/tasks?page=1&limit=20&status=FAILED`：按创建时间倒序列出任务，`limit` 最大 100，`status` 可选。`data` 为 `{items, page, limit, total}`，`total` 是所有页的任务数。列表不带下载链接。
- `GET /v1/usage?month=2026-11`：某月（UTC）用掉的处理单位、未结束任务预占的单位、存储占用与配额，以及按天的明细；需要 `tasks:read`。普通转换算 1 个单位，按体积目标搜索按实际编码轮数算，复用已有产物与失败不算，见[契约 §用量与配额](/contracts/http-api#用量与配额)。
- 完整的端点表见[契约 §端点](/contracts/http-api#端点)。

## 通过 MCP 接入

同一个 API 服务在 `/mcp` 提供远程 MCP 服务器（Streamable HTTP），用同样的 API Key 认证，提供三个工具。它调用的是与 `/v1` 相同的服务，scope、限流、配额与幂等规则都相同。逐字段的定义见 [MCP 契约](/contracts/mcp)。

### 配置客户端

MCP 端点不支持 OAuth，把 key 写进请求头。以 Claude Code 为例：

```bash
claude mcp add --transport http lightyear "$LIGHTYEAR_API/mcp" --header "Authorization: Bearer $LIGHTYEAR_API_KEY"
```

其他客户端的配置文件一般写成：

```json
{
  "mcpServers": {
    "lightyear": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "Authorization": "Bearer ly_…" }
    }
  }
}
```

只查结果的 Agent 用 `tasks:read` 的 key 就够；要上传和提交，key 需要 `tasks:read` 与 `tasks:write` 两个权限。

### 调用流程

1. **取得源图。** 图片在公网上时，直接把 URL 交给 `submit_task`，跳过这一步。本地文件先调用 `create_upload`：

   ```json
   {"filename": "photo.jpg", "size": 557596, "content_type": "image/jpeg", "sha256": "…"}
   ```

   再由 Agent 用自己的 HTTP 工具把字节发到结果里的预签名地址，例如 `method` 为 `put` 时：

   ```bash
   curl -sS --fail -X PUT --upload-file photo.jpg "$URL"   # 结果里的 url，不带 Authorization 头
   ```

   图片内容不经过工具参数，所以 Agent 需要能执行 HTTP 请求（如 shell 里的 `curl`）。没有这个能力的 Agent 只能用 URL 源。`method` 为 `multipart` 时按 `part_size` 切片，依次 `PUT` 到 `part_urls`；为 `none` 时不用上传。上传后不需要通知完成。

2. **提交任务。** `submit_task`：

   ```json
   {
     "items": [{"file_id": "…"}, {"url": "https://example.com/cat.jpg"}],
     "outputs": [
       {"name": "small", "options": {"format": "avif"}, "target": {"max_bytes": 60000}},
       {"name": "web", "options": {"format": "webp", "quality": 80, "resize": {"width": 1024}}}
     ],
     "idempotency_key": "photo-batch-1"
   }
   ```

   上传还没完成就提交，会得到 `error.param.invalid`（`reason: "upload not finished"`），补传后再提交即可。带上 `idempotency_key` 后，调用超时、结果没拿到时可以原样重试，不会建出第二个任务。

3. **等待并下载。** `get_task`，`wait_seconds` 最多 30：

   ```json
   {"task_id": "…", "wait_seconds": 30}
   ```

   任务一结束就返回，否则等满后返回当前状态，再调用一次即可。完成的 job 带 `output_file.download_url`（1 小时内有效）与 `compression`，用 HTTP 客户端下载。

失败时工具返回 `isError: true`，内容是 `{"code", "message", "details"}`，`code` 与上文[错误处理](#错误处理)里 `/v1` 的相同；限流时 `details.retry_after` 给出建议等待的秒数。

### 用 MCP Inspector 验证

```bash
npx @modelcontextprotocol/inspector --cli "$LIGHTYEAR_API/mcp" --transport http \
  --header "Authorization: Bearer $LIGHTYEAR_API_KEY" --method tools/list
```

把 `--method tools/list` 换成 `--method tools/call --tool-name get_task --tool-arg task_id=… --tool-arg wait_seconds=30` 即可调用工具。
