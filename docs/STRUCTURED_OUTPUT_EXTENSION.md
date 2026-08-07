# 可选结构化输出接收扩展

该扩展只在 Wiki 与 Graph 的模型响应进入原解析器之前做验收。它不接管模型调用、文档 CRUD、
Wiki retract/delete、Graph 清理、任务队列或数据库写入。

## 构建与模式

标准构建不导入扩展实现：

```bash
make build-prod
```

定制构建显式加入标签：

```bash
make build-prod GO_BUILD_TAGS=weknora_structured_output
```

Docker Compose 构建时在 `.env` 中设置：

```dotenv
WEKNORA_GO_BUILD_TAGS=weknora_structured_output
```

运行模式始终默认 `off`：

- `off`：不调用扩展，继续使用源码原有解析链路。
- `shadow`：执行验收并记录有界元数据，但强制把原响应交给源码解析，且不改变任务结果。
- `enforce`：验收成功后把规范化 JSON 交给原解析器；验收失败返回原任务链路处理。

切换模式需要重启 app 进程；不需要数据库迁移。

## Gateway 配置

不要按 `base_url`、主机名或端口自动判断 Gateway。按 WeKnora 模型 UUID 显式声明：

```dotenv
WEKNORA_STRUCTURED_OUTPUT_MODE=shadow
WEKNORA_STRUCTURED_OUTPUT_ACCEPTANCE=compatibility
WEKNORA_STRUCTURED_OUTPUT_GENERATION_OWNER=none
WEKNORA_STRUCTURED_OUTPUT_MODEL_RULES={"MODEL_UUID":{"acceptance":"strict","generation_owner":"gateway"}}
WEKNORA_STRUCTURED_OUTPUT_LLM_TIMEOUT_SECONDS=300
```

`generation_owner=gateway` 仅声明职责边界。首期扩展不会注入 Schema、修改 prompt 或再次调用
模型，因此不会与 `weknora-agent-gateway` 形成双重生成侧处理。`acceptance=strict` 只接受单一完整
JSON，同时仍允许可证明无歧义的业务归一化，例如唯一裸 slug 映射和已知 Graph 字段别名。

只有非 Schema、非 Gateway 的兼容模型才应使用 `acceptance=compatibility`。兼容模式可处理完整
围栏、前后说明、控制字符、非法反斜杠和尾逗号，但绝不补全截断 JSON。

## 接收合同

Wiki 覆盖候选实体/概念、chunk 引用、去重与 taxonomy。引用 slug 必须能在本次候选表中唯一
解析，chunk handle 必须属于当前批次。候选或引用验收失败在 `enforce` 下会返回 Wiki 原重试
链路，不能再静默发布无引用页面。

Graph 覆盖当前文档图数组、规范 `{nodes,relations}` 对象，以及 legacy entity/relation 入口。
已知别名会规范化后交还现有 `Formater.ParseGraph` 或 `ParseLLMJsonResponse`；非空但无法识别的
结构会失败，显式空对象/数组仍表示没有图事实。

Wiki/Graph 的空响应、`finish_reason=length` 以及超过独立调用超时的请求只在 `enforce` 下阻止
继续处理；`shadow` 不改变原任务。

## 建议启用顺序

1. 构建带标签镜像，但保持 `WEKNORA_STRUCTURED_OUTPUT_MODE=off`，验证标准行为。
2. 对目标 Gateway 模型配置 `strict + gateway`，切换为 `shadow`，只查看合同、字符数、SHA-256、
   策略、归一化计数和错误码；日志不包含模型原文。
3. 审核 shadow 结果后切换为 `enforce`，只重新排队一份 Wiki 文档。
4. 验证摘要非空、引用页面 `chunk_refs > 0`，再对一个历史 Graph 失败 chunk 做 canary。
5. 审核通过前不要批量 replay。

## 回滚

最快回滚是把 `WEKNORA_STRUCTURED_OUTPUT_MODE` 改为 `off` 并重启 app；这会立即恢复原解析链路，
不会修改或回滚数据库。彻底移除时重新构建不带 `weknora_structured_output` 标签的标准镜像即可。
