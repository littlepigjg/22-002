# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
在固件升级进度上报场景中，当使用重试机制（retry.Do）进行操作重试时，通过回调收集到的错误历史条目数量少于实际重试次数。配置最大重试 3 次且每次重试都失败的情况下，只能收集到 1 条错误记录，前两次重试的错误信息丢失，影响问题诊断和故障分析能力。

## 2. 环境信息（Environment）
- 操作系统：Linux
- Go 版本：go1.23.x
- 项目模块/依赖：firmware-upgrade
- 运行参数：go test -v -run '^TestRedGreen$' .
- 硬件信息：CPU 多核

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 执行 `go test -v -run '^TestRedGreen$' .` 运行测试用例
3. 观察测试输出和日志信息

## 4. 实际结果（Actual Behavior / Observed Output）
- 测试输出：
  ```
  RED（红灯，缺陷未修复）
  预期收集 3 个错误，实际收集 1 个错误
  ```
- 日志信息：
  ```
  update history failed after retry
  ```
- RED/GREEN 判定结果：RED
- 收集到的错误数量：1（预期应为 3）

## 5. 期望结果（Expected Behavior）
- 测试输出：
  ```
  GREEN（绿灯，缺陷已修复）
  ```
- 收集到的错误数量：3（与配置的 MaxAttempts 一致）
- 日志中不应出现异常警告
- go build ./... 编译通过
- go vet ./... 无警告

## 6. 触发频率（Frequency）
必现（100%）：每次调用重试机制且重试次数大于 1 时，都会出现错误收集不完整的问题。

## 7. 影响范围（Impact / Scope）
- 影响固件升级进度上报服务的重试错误收集功能
- 影响故障诊断能力，无法完整记录每次重试的错误信息
- 影响运维人员对重试历史的分析和排查效率
- 不影响核心业务逻辑的执行（重试本身正常工作）

## 8. 附加说明（Additional Notes / Workaround）
当前版本无临时 workaround。如需获取完整的重试错误历史，可考虑在重试外部自行包装一层错误收集逻辑，但这会增加代码复杂度。
