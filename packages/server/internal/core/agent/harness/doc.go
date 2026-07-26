// Package harness 为 core/agent/react 提供企业级横切能力：可靠性、治理、可观测、确定性。
//
// 通过装饰 llm.Client、tool.Tool 和组合 agent.Hooks 实现，不修改 react 主循环。
// 依赖方向严格由外向内：仅依赖 core/llm、core/tool、core/agent 及其子包，不引用
// HTTP handler、具体数据库适配器或 logic 层。
package harness
