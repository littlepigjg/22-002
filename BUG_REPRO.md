# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）

固件升级服务在处理文件上传和下载时存在路径安全漏洞。当固件存储目录的配置路径不以斜杠（/）结尾时，包含 "../" 路径遍历序列的文件名可以绕过安全检查，导致文件被写入到固件存储目录之外的位置，或允许读取目录外的文件。

## 2. 环境信息（Environment）

- 操作系统：Linux
- Go 版本：go version go1.23.4 linux/amd64
- 项目模块：firmware-upgrade
- 运行参数：go test . -count=1 -run '^TestRedGreen$'
- 硬件信息：CPU 核数不影响复现

## 3. 复现步骤（Steps to Reproduce）

1. 进入项目根目录：cd /home/admin/code/22/22-002-30
2. 确保编译通过：go build ./...
3. 执行测试命令：go test . -count=1 -run '^TestRedGreen$'
4. 观察测试输出中的 RED/GREEN 判定结果

## 4. 实际结果（Actual Behavior / Observed Output）

- 测试输出：
  - Test "SafeJoin should reject traversal when baseDir no trailing sep"：FAIL (RED)，显示 SafeJoin 允许了路径逃逸
  - Test "SafeJoin should work with normal file when baseDir no trailing sep"：PASS (GREEN)，正常文件不受影响
  - Test "SafeJoin should reject traversal with pre-cleaned path"：FAIL (RED)，预清理后的路径仍能逃逸
  - Test "JoinWithFallback should handle no-trailing-sep correctly"：PASS (GREEN)，备选API正常
  - Test "SafeJoinFlex should reject traversal"：PASS (GREEN)，备选API正常
  - Test "SafeJoinDir should enforce trailing sep"：PASS (GREEN)，备选API正常
- 总体判定：RESULT: RED (bug exists - some tests failed)
- 退出码：1（FAIL）

## 5. 期望结果（Expected Behavior）

- 所有测试用例均应通过（PASS GREEN）
- SafeJoin 无论 baseDir 是否以分隔符结尾，均应拒绝包含 "../" 的路径
- 正常文件名的拼接操作不受影响
- 总体判定：RESULT: GREEN (bug fixed - all tests passed)
- go build ./... 编译通过
- go vet ./... 无报错

## 6. 触发频率（Frequency）

必现（100%）：只要 baseDir 不以分隔符结尾且文件名包含 "../" 路径序列，必定触发路径逃逸。

## 7. 影响范围（Impact / Scope）

该漏洞可能导致：
- 固件文件被写入到固件存储目录之外的任意位置
- 任意文件被覆盖（如果路径指向已存在的文件）
- 通过下载接口读取系统敏感文件
- 攻击者可能覆盖系统关键文件，造成系统损坏
- 数据完整性和机密性风险

## 8. 附加说明（Additional Notes / Workaround）

临时规避方法：确保固件存储目录配置始终以斜杠（/）结尾。例如将 "/data/firmware" 改为 "/data/firmware/"。但这只是临时方案，根本修复需要在代码层面加强路径验证。