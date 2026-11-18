# video-lt 视频接入

在 Sub2API 新建 `platform=video-lt` 的分组和 API Key 类型的上游账号。后台新建时预填分组名 `video-lt`，模型列表使用 `owned_by: "video-lt"`。账号填 HFsyAPI Key，Base URL 默认 `https://www.hfsyapi.cn`。开启出站域名白名单的部署需允许该域名。连接测试和模型同步只读取上游 `/v1/models`。

在分组的「视频生成计费」为需要开放的模型填写美元单价。支持 Seedance 2.5 的 480p、720p、1080p，Flash 的 480p、720p，Seedance 2 的 fast、mini、vip 档，以及 `minimax-h3`。模型可使用账号映射公开别名。Seedance 单价用 USD/秒，H3 的 720p 单价用 USD/次。模型单价从公开别名或实际上游 ID 读取，成功任务按提交时保存的价格和倍率结算一次。Seedance 使用请求的输出时长，H3 使用一次生成。HFsyAPI 的公开人民币价格不会自动换算成分组售价。

| 操作 | 接口 |
| --- | --- |
| 获取模型 | `GET /v1/models` |
| 创建 | `POST /v1/video/create` |
| 查询 | `GET /v1/video/query/{id}` 或 `POST /v1/video/query/{id}`。同样支持 `/v1/video/query?id={id}`，查询不需要请求体 |

HFsyAPI 独立分组也可使用 `POST /v1/videos/generations` 和 `GET /v1/videos/{id}`，请求和响应仍采用 HFsyAPI JSON。组合分组使用上表的原生接口。

当 iAgent 站点代理到同一套 Sub2API 时，客户端可在两个域名间沿用上表的原生路径、JSON、同一把分组 Key 及已创建任务的 ID。直连 HFsy 官方服务时使用 HFsy Key 和上游模型 ID。

创建时以原生 JSON 转发选项，如 `size` 和 `watermark`。账号映射仅替换 `model`。媒体验证、计费和任务归属仍按本平台的分组与令牌执行。

所有请求使用同一把绑定了分组的 Sub2API Key。创建返回 `id`。查询在 `data.status` 返回 `queued`、`in_progress`、`completed` 或 `failed`。成功后 `data.video_url` 和 `data.result_url` 提供同一个视频地址，失败原因见 `data.fail_reason`。已受理任务会在后台查询和结算，提交者可以断开连接。任务查询始终与原用户、分组、Key 和上游账号绑定。保留创建返回的 ID，后续使用它查询。

## Seedance 2.5 Flash

以下请求生成一段 4 秒文生视频。

```json
{
  "model": "sd-2.5-flash-480",
  "prompt": "树叶在风中轻轻摆动",
  "orientation": "landscape",
  "duration": 4
}
```

普通模式使用 `orientation`（`landscape` 或 `portrait`），Seedance 可用 `ratio` 选更细的比例。首帧模式传 `start_image_url`，首尾帧模式再传 `end_image_url`。首帧决定画幅，此模式省略 `orientation`、`ratio`、`images`、`videos` 和 `audios`。

```json
{
  "model": "sd-2.5-720",
  "prompt": "镜头从首帧缓慢推进至尾帧",
  "duration": 5,
  "start_image_url": "https://example.com/start.png",
  "end_image_url": "https://example.com/end.png"
}
```

## MiniMax H3

以下请求使用一张参考图生成 5 秒视频。把图片地址替换为自己的素材地址。

```json
{
  "model": "minimax-h3",
  "prompt": "@image1 兔子轻轻抬头，固定镜头",
  "orientation": "landscape",
  "images": ["https://example.com/rabbit.png"],
  "duration": 5
}
```

MiniMax H3 用 `orientation` 选择横竖屏。上游创建示例也支持 `size` 和 `watermark`。

## 模型与素材限制

| 模型 | 输出 | 图片 / 视频 / 音频 | 首帧与首尾帧 | 提示词上限 |
| --- | --- | --- | --- | --- |
| `sd-2.5-480`、`sd-2.5-720`、`sd-2.5-1080` | 480p、720p、1080p，4–30 秒 | 30 / 10 / 10 | 支持 | 10,000 字 |
| `sd-2.5-flash-480`、`sd-2.5-flash-720` | 480p、720p，4–30 秒 | 10 / 5 / 5 | 支持 | 10,000 字 |
| `sd-2-fast-480`、`sd-2-fast-720` | 480p、720p，5–15 秒 | 9 / 3 / 3 | — | 10,000 字 |
| `sd-2-mini-480`、`sd-2-mini-720` | 480p、720p，5–15 秒 | 9 / 3 / 3 | — | 10,000 字 |
| `sd-2-vip` / `sd-2-vip-480`、`sd-2-vip-720` | 两个 480p 名称及 720p，5–15 秒 | 9 / 3 / 3 | — | 10,000 字 |
| `minimax-h3` | 720p，5–15 秒 | 1–9 / 0 / 3 | — | 7,000 字 |

- Seedance 2.5 系列支持 16:9、9:16、4:3、3:4、1:1。Seedance 2 另外支持 21:9。H3 使用 16:9 或 9:16。
- Seedance 2 Fast、Mini、VIP 使用参考视频时，至少同时提供一张参考图。Seedance 2.5、Flash 和 Seedance 2 VIP 的参考素材须在提示词中引用。编号按各类数组从 1 开始，可写 `@image1`、`@video1`、`@audio1` 或 `@图片1`、`@视频1`、`@音频1`。
- Seedance 支持 JPG、PNG、WebP 和 GIF 图片，参考图片合计不超过 30 MB，单张建议不超过 20 MB。H3 图片单张上限为 20 MB。
- 参考视频支持 MP4 和 MOV，单个 3–10 秒、不超过 200 MB，上游文档标注分辨率 720–2160 px。参考音频支持 MP3、WAV、M4A、AAC、OGG、WebM 等常见格式，单段 3–10 秒、不超过 15 MB。视频和音频按各自类别计算总时长，各不超过 15 秒。
- 按单段 3 秒和累计 15 秒计算，参考视频和参考音频每类实际最多 5 段。API 在转发前拒绝按最低时长计算也会超限的请求。
- Seedance 2 Fast 和 VIP 的新请求以各型号的 API 参数页为准，从 5 秒起。历史已受理的 4 秒任务仍按原请求时长结算。

公开 API 的素材字段使用可公开访问的 HTTP(S) 地址，素材实际内容由 HFsyAPI 校验。iAgent 工作台支持填写已有 URL，或由浏览器把本地素材直传站点已配置的 R2。生成请求将 R2 的公开链接交给 HFsyAPI，素材按站点的参考素材保留策略清理。

[Seedance 2.5 上游文档](https://www.hfsyapi.cn/docs/api?model=sd-2.5-1080) · [Seedance 2 Fast 上游文档](https://www.hfsyapi.cn/docs/api?model=sd-2-fast-480) · [Seedance 2 VIP 上游文档](https://www.hfsyapi.cn/docs/api?model=sd-2-vip) · [MiniMax H3 上游文档](https://www.hfsyapi.cn/docs/api?model=minimax-h3) · [价目表](https://www.hfsyapi.cn/pricing)
