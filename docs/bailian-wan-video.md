# 阿里云百炼：Wan 3 原生视频接入

`bailian` 是独立平台，可创建自己的分组、API Key 账号、模型映射、视频定价和用户平台额度。它与 Grok 平级，使用百炼原生异步协议，不依赖 Grok 的响应结构或 15 秒计费限制。本次范围是 `wan3.0-video`、`wan3.0-video-prime` 的全部视频生成模式。

容器部署可使用 `ghcr.io/nbb2025/sub2api:wan3`，自动发布与现有实例切换步骤见 [镜像发布与升级](container-images.md)。

## 管理员配置

1. 新建 **Alibaba Cloud Bailian / 阿里云百炼** 分组。
2. 在视频计费区明确配置 **USD/秒** 单价，支持 480p、720p、1080p 和逐模型覆盖。标准版与高速版可分别定价，自定义公开别名也可独立定价。优先匹配公开别名的覆盖价，再匹配账号映射后的 Wan 模型，最后使用通用分辨率价格。
3. 新建 **阿里云百炼** API Key 账号，填写同地域的百炼 Key 和业务空间根地址，例如 `https://WORKSPACE_ID.cn-beijing.maas.aliyuncs.com`。其他地域按阿里云文档选择；也接受以 `/api/v1` 结尾的基址。
4. 将账号绑定至百炼分组，为调用方创建绑定该分组的 Sub2API Key。

模型同步使用百炼 `GET /api/v1/models`，按 `providers=wan`、`capabilities=VG` 分页查询并筛选上述 Wan 3 模型。账号连接测试只检查鉴权和模型目录，不创建收费视频，不代表已完成真实生成测试。

阿里云价格页中的人民币单价不自动换算成 Sub2API 美元钱包单价。正常计费模式下，未配置单价会在提交任务前返回 `billing_configuration_error`；显式配置 `0` 表示免费。视频独立倍率、用户专属分组倍率、余额/订阅扣减、Key 额度及现有平台额度机制继续复用。

创建时保存分辨率价格、计费模型、有效视频倍率、账号倍率及当次订阅 ID。任务生成期间管理员改价或订阅到期，不会把已受理任务改为新的价格或另一个订阅。

简易模式沿用应用不扣钱包的行为；若明确启用简易模式 Key 金额限流，仍需要有效价格，不能从视频秒数推算 Token 或自动选择汇率。

## 对外接口

Base URL 为 Sub2API 根地址。下面接口的 `Authorization` 使用 **Sub2API Key**，不向调用方暴露百炼供应商 Key。

| 方法 | 路径 | 功能 |
| --- | --- | --- |
| POST | `/api/v1/services/aigc/video-generation/video-synthesis` | 创建原生视频任务 |
| GET | `/api/v1/tasks/{task_id}` | 查询任务状态、结果与原始用量 |
| GET | `/api/v1/models` | 百炼格式的当前分组模型列表 |
| GET | `/v1/models` | 通用模型列表格式 |

原生模型列表支持 `model`、`name`、`page_no`、`page_size`。列表来自本地分组可见模型，不对普通用户透传供应商的价格目录。

创建请求中只有 `model` 会按账号映射改写；`input`、`parameters` 及扩展字段保持原样。服务端自动添加 `X-DashScope-Async: enable`，出现 OSS 媒体地址时自动添加 `X-DashScope-OssResourceResolve: enable`。

```bash
curl 'https://YOUR_SUB2API/api/v1/services/aigc/video-generation/video-synthesis' \
  -H "Authorization: Bearer $SUB2API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "wan3.0-video-prime",
    "input": {
      "prompt": "参考视频1的运镜和音频1的节奏，生成一段20秒的视频",
      "media": [
        {"type":"reference_video","url":"https://example.com/reference.mp4"},
        {"type":"reference_audio","url":"https://example.com/reference.mp3"}
      ]
    },
    "parameters": {
      "duration": 20,
      "resolution": "1080P",
      "ratio": "16:9",
      "audio": true,
      "seed": 0,
      "prompt_extend": true,
      "watermark": false
    }
  }'
```

此示例参考视频总时长不得超过 10 秒。无视频输入时可请求 30 秒输出。

## 参数与任务模式

| 字段 | 规则 |
| --- | --- |
| `model` | `wan3.0-video`、`wan3.0-video-prime` 或配置的公开别名 |
| `input.prompt` | 文本提示词；与 `input.media` 至少提供一个。服务端不自行截断原始文本；供应商的提示词长度限制仍适用 |
| `input.media` | 媒体对象数组，每项为 `{"type":"...","url":"..."}`，最多 20 项 |
| `parameters.duration` | 无视频输入时为 2–30 秒整数，`-1` 表示智能时长；原生默认 5 秒。有视频输入时不套用无视频输入的 2 秒下限，输入与输出总时长不超过 30 秒，具体素材约束由百炼校验 |
| `parameters.resolution` | `480P`、`720P`、`1080P`，原生默认 `1080P` |
| `parameters.ratio` | `adaptive`、`21:9`、`16:9`、`4:3`、`1:1`、`3:4`、`9:16`，默认 `adaptive` |
| `parameters.audio` | 布尔值，默认 `true`。控制输出音轨，声音开关不另加价 |
| `parameters.seed` | `-1` 或 0–2147483647 的整数。未传/`-1` 使用随机种子 |
| `parameters.prompt_extend` | 布尔值，默认开启。文件或网页输入不能显式关闭 |
| `parameters.watermark` | 布尔值，默认关闭 |

| 模式 | 媒体类型与限制 |
| --- | --- |
| 文生视频 | 只提供提示词 |
| 首帧生视频 | `first_frame`，最多 1 张 |
| 首尾帧生视频 | `first_frame` + `last_frame`，各最多 1 张 |
| 图片参考 | `reference_image`，最多 10 张 |
| 视频参考 | `reference_video`，最多 5 段，输入视频总时长不超过 15 秒 |
| 音频参考 | `reference_audio`，最多 5 段，输入音频总时长不超过 15 秒 |
| 组合参考 | 参考图片、视频、音频可组合 |
| 文件/网页参考 | `file` 或 `link`，最多 1 个，不能同时提供，可与参考媒体组合 |
| 视频编辑 | 参考视频 + 编辑提示词，可配合图片/音频参考和 `duration=-1` |
| 视频延长 | 参考视频 + 延长提示词，通常使用 `ratio=adaptive`；时长仍受供应商总量约束 |

首尾帧模式不能与参考图片、视频、音频、文件或网页混用。图片支持 HTTP(S)、OSS 与图片 Base64 data URI；视频、音频、文件使用 HTTP(S)/OSS 地址；网页使用公开 HTTP(S) 地址。远程媒体的格式、文件大小、分辨率、实际时长和输入输出总时长由百炼校验，Sub2API 不下载整份输入素材来猜算用量。

编辑与延长均使用同一个原生创建入口，通过提示词和 `input.media` 表达，不要求转换成 Grok 编辑接口。

## 任务 ID、轮询与错误

创建及查询的成功响应保持百炼原始 JSON。请使用 `output.task_id`，顶层 `request_id` 是 HTTP 请求追踪号。

```json
{
  "request_id": "http-trace-id",
  "output": {
    "task_id": "video-task-id",
    "task_status": "PENDING"
  }
}
```

```bash
curl 'https://YOUR_SUB2API/api/v1/tasks/video-task-id' \
  -H "Authorization: Bearer $SUB2API_KEY"
```

状态保留 `PENDING`、`RUNNING`、`SUCCEEDED`、`FAILED`、`CANCELED`、`UNKNOWN`。成功结果从 `output.video_url` 获取，可直接下载至自己的持久存储。任务及结果链接按阿里云规则有效 24 小时。

查询仅允许创建任务的用户、Sub2API Key、分组和绑定的百炼账号；不能改用另一个 Key/供应商查询。任务归属与创建快照持久化在 PostgreSQL，并复用 Redis 查询缓存；缓存丢失时按用户、Key、分组及 24 小时窗口从数据库恢复。缺少有效用量时不默认按 5 秒、8 秒或零费用处理。

余额或 Key 额度耗尽后，仍可鉴权查询自己的在途任务，首次成功查询仍执行原任务结算；被停用的 Key、用户或分组继续拒绝。创建新任务仍执行正常余额、额度与订阅准入。

创建任务遇到超时、连接断开、429、5xx 或无效响应不自动重建或换账号重试，因为上游可能已经接受任务。轮询同一任务不会重新生成视频。原生成功响应在任务归属和计费快照写入之后才下发，避免客户端立即轮询时找不到任务。

## 计费与精度

客户端查询和后台 worker 都可观察任务成功并发起同一笔结算。仅 `SUCCEEDED`、有效结果 URL 和有效用量满足结算条件；其他任务状态不收费。创建本身不扣视频费用。

官方计费口径为：

```text
计费秒数 = usage.duration
         = usage.input_video_duration + usage.output_video_duration

原始费用 = 显式配置的 USD/秒单价 × 计费秒数
实际费用 = 原始费用 × 有效视频倍率
```

例如参考视频 5 秒、输出视频 10 秒，应计费 15 秒；纯输出 30 秒应计费 30 秒。参考图片、参考音频和输出音轨不另外添加自造计费单位，也不将秒数换成 Token。

三个时长以十进制数解析和计算，不截整数、不套用 Grok 的 15 秒上限。阿里云返回的总秒数是计费依据；仅容忍浮点 JSON 序列化带来的极小一致性误差，不据此重写上游数值。

金额在最终结算边界按 Sub2API 既有 `NUMERIC(20,8)` 精度量化，使用 half-away-from-zero。余额、Key 额度、订阅用量使用同一金额。成功结果在数据库结算提交后才下发。重复轮询通过 Redis 短租约和数据库 `usage_billing_dedup` 共用稳定任务标识，只扣一次；“结算中”不会被当作“已结算”。数据库结算失败时返回可重试错误并释放认领，进程中断后短租约到期也可重查。

结算失败时不先写入零费用用量行；若金额已提交但用量日志写入失败，重查可通过数据库去重补写原日志，避免重复扣费和零费用记录占用任务的唯一日志键。

原生成功用量示例：

```json
{
  "video_count": 1,
  "duration": 11.500000001,
  "input_video_duration": 5.125000001,
  "output_video_duration": 6.375,
  "fps": 30,
  "SR": 1080,
  "ratio": "16:9"
}
```

用量日志另存 `video_usage` JSONB，保留输入、输出、总秒数和实际分辨率。旧 Grok 的 `video_duration_seconds` 整数列保持原有语义，不用于百炼。

管理端/用户用量 API 的新增 `video_usage` 时长字段以**十进制字符串**返回，防止浏览器把高精度 JSON 数字转成 JavaScript Number 时丢失末位。原生百炼查询响应的数字格式保持不变。日志界面与 CSV/XLSX 导出分别展示三种时长；百炼金额展示及导出保留 8 位小数。

调用方停止轮询后，后台 worker 仍查询本应用已受理的任务并结算，不扫描供应商其他用户的任务，也不重新创建视频。任务使用 PostgreSQL 租约与 token 防护并发覆盖，支持进程重启及多个副本；每实例每批最多 8 个、并发最多 8 个，复用账号并发槽，429 尊重 `Retry-After`。未完成任务通常约一分钟再查，排队或上游退避可延后。

首次观察到的成功用量在结算前持久化，数据库结算或日志故障恢复后可继续，不依赖结果 URL 继续有效。未观测到成功状态的过期任务不会被推定成功。终态任务元数据保留 7 天后有界清理，已知用量但尚未结算的任务保留重试；费用账本及用量日志继续使用既有保留规则。不在任务元数据中保存提示词、媒体地址或 API Key。

## 验证范围

测试覆盖原生多模态请求、30 秒与智能时长、OSS 请求头、模型映射及分页同步、跨用户/Key/分组/供应商的任务隔离、缺失/异常用量拒绝、Decimal 金额计算、日志精度及真实 PostgreSQL 的重复扣费去重。

连接测试和本地模拟上游测试不等同于真实百炼生成。上线前应使用所属地域的真实业务空间与 API Key 验证素材、模型权限和实际返回。

官方依据：[Wan 3 API 参考](https://help.aliyun.com/zh/model-studio/wan3-video-generation-api-reference)、[Wan 3 使用指南](https://help.aliyun.com/zh/model-studio/wan3-video-generation-guide)、[模型调用价格](https://help.aliyun.com/zh/model-studio/model-pricing)、[模型列表 API](https://help.aliyun.com/zh/model-studio/list-models)。
