// Package firmwareupgrade 实现一个固件升级管理服务：提供固件元数据管理、设备管理、升级任务、灰度策略、统计与诊断能力。
// 所有 HTTP 入口位于 internal/handler，存储实现位于 internal/store，服务层位于 internal/service，公共工具位于 pkg。
package firmwareupgrade

