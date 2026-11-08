# 镜像发布与升级

此 Fork 使用 [Publish container image](https://github.com/nbb2025/sub2api/actions/workflows/publish-image.yml) 自动发布 `ghcr.io/nbb2025/sub2api`，支持 `linux/amd64`、`linux/arm64`。

| Git 分支 | 推送成功后更新的镜像标签 |
| --- | --- |
| `codex/bailian-wan-video` | `ghcr.io/nbb2025/sub2api:wan3` |
| `main` | `ghcr.io/nbb2025/sub2api:latest` |

每次构建还发布 `sha-完整提交SHA` 标签。Actions 的 `container-image-release` 附件与任务摘要提供该次完整镜像摘要，例如 `ghcr.io/nbb2025/sub2api@sha256:...`。生产环境建议固定该摘要；移动标签只在下一次主动拉取、重建应用容器时更新。Wan 3 分支没有合入 `main` 时，请使用 `wan3` 或它对应的摘要。

## 自动构建

工作流使用本次提交构建，先运行后端单元测试与 Wan 3 PostgreSQL 计费集成测试，再在两个原生架构 Runner 上分别编译前后端、发布架构摘要，并验证应用版本、提交号和 PostgreSQL 备份工具。两个架构都通过后才更新上述标签，同时发布 SBOM 与构建来源证明。完整的 CI 和安全扫描也会在推送时运行，可在 Actions 查看各自结果。

发布只使用工作流自带的 `GITHUB_TOKEN`，发布任务取得 `packages: write`；不需要把个人 Git/gh 凭据写入仓库或创建 PAT Secret。工作流仅允许此 Fork 的上述两个分支发布镜像；合入默认分支后也可在 Actions 中选择这两个分支手动运行。已有 `v*` 版本标签发布流程继续保留。

GitHub 首次创建 GHCR 包默认是私有。若重新创建或迁移仓库，管理员需在包设置里将可见性设为 **Public**，才能匿名拉取；公开代码仓库不会自动使包公开。可在 [容器包页面](https://github.com/users/nbb2025/packages/container/package/sub2api) 检查可见性。参考 [GitHub GHCR 文档](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

自动发布镜像不会自动连接服务器或重启现有实例。

## 已有 Docker Compose 实例切换

在**原部署目录**操作，保持原 Compose 文件、项目名称、`.env`、数据卷及挂载路径。升级前按现有流程备份 PostgreSQL 和应用配置；新版本启动时会自动执行数据库迁移。若数据库服务名为 `postgres`，可在该部署使用的 Compose 命令后执行：

```bash
mkdir -p backups
docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > "backups/pre-wan3-$(date +%Y%m%d-%H%M%S).dump"
```

外部数据库使用其现有备份方式。保留 `JWT_SECRET`、`TOTP_ENCRYPTION_KEY` 等配置及应用数据，确认备份完成后再切换。

### 已有文件中的镜像写死时

下载只包含应用镜像的覆盖文件；下面以原文件名 `docker-compose.yml` 为例。若原来使用 `docker-compose.local.yml`、多份 `-f` 文件或 `-p` 项目名，所有命令继续使用原参数，并将镜像覆盖文件作为**最后一份** `-f`。

```bash
curl -fL https://raw.githubusercontent.com/nbb2025/sub2api/codex/bailian-wan-video/deploy/docker-compose.image.yml \
  -o docker-compose.image.yml

# 可将 wan3 换成本次发布的 @sha256:... 完整镜像地址。
export SUB2API_IMAGE=ghcr.io/nbb2025/sub2api:wan3

docker compose -f docker-compose.yml -f docker-compose.image.yml config --images
docker compose -f docker-compose.yml -f docker-compose.image.yml pull sub2api
docker compose -f docker-compose.yml -f docker-compose.image.yml up -d --no-deps sub2api
docker compose -f docker-compose.yml -f docker-compose.image.yml ps sub2api
```

这组命令只更新 `sub2api`，数据库和 Redis 沿用当前容器。之后应将 `SUB2API_IMAGE` 写入现有 `.env`，并在后续管理命令中继续携带镜像覆盖文件。更新操作无需 `down`，更不要使用 `down -v` 删除持久化数据。

### 已采用本分支新版 Compose 模板时

三个生产模板均支持 `SUB2API_IMAGE`，默认仍为原上游镜像。在现有 `.env` 设置 `SUB2API_IMAGE=ghcr.io/nbb2025/sub2api:wan3`（或完整摘要），使用原 Compose 命令执行 `pull sub2api`、`up -d --no-deps sub2api` 即可。仅修改 `.env` 不会改变旧版写死的 `image:`，旧版请使用上面的覆盖文件。

## 验证与回退

```bash
# 以下 compose 参数必须与切换时完全相同。
docker compose -f docker-compose.yml -f docker-compose.image.yml exec -T sub2api /app/sub2api --version
docker compose -f docker-compose.yml -f docker-compose.image.yml logs --tail=80 sub2api
```

版本输出的提交号应与 Actions 发布记录一致，容器应通过健康检查，管理后台新建分组应能选择“阿里云百炼”。随后按 [Wan 3 接入与计费说明](bailian-wan-video.md) 配置百炼账号、分组价格和调用方 Key。

升级前记录当前应用镜像摘要。需要回退时，先停止新建百炼任务并等待已有任务完成、结算，再将 `SUB2API_IMAGE` 改为原摘要，重复 `pull sub2api` 和 `up -d --no-deps sub2api`。旧镜像没有百炼平台与后台结算能力，回退期间不能继续处理百炼任务；不要自动回滚或删除数据库。跨版本数据库兼容性按所用版本及升级前备份处理。
