// Package firmware_upgrade is the root module for the firmware upgrade platform.
//
// The platform provides services for firmware management, upgrade task scheduling,
// device polling, execution tracking, progress reporting and statistics.
//
// Entry point: cmd/server/main.go assembles HTTP server, stores and services.
//
// Sub-packages:
//   - internal/config: configuration loader
//   - internal/model:  entities, DTOs and errors
//   - internal/store:  in-memory store implementations and interfaces
//   - internal/service: business services (task, poll, progress, history, etc.)
//   - internal/handler: HTTP handlers / router
//   - pkg/safemap:     generic concurrent-safe map
//   - pkg/*:           utility packages
package firmware_upgrade
