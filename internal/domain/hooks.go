package domain

import "errors"

var ErrInvalidHook = errors.New("invalid observation hook")

type HookPoint struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// HookPoints describes committed events, not execution permissions or evidence.
func HookPoints() []HookPoint {
	return []HookPoint{
		{"mission.created", "任务创建", "任务已创建；事件发生不等于验证成功。"},
		{"plan.ready", "计划就绪", "计划已生成；事件发生不等于验证成功。"},
		{"plan.revised", "计划修订", "计划已修订；事件发生不等于验证成功。"},
		{"plan.approved", "计划批准", "计划审批已提交；事件发生不等于验证成功。"},
		{"candidate.edited", "候选编辑", "候选内容已编辑；事件发生不等于验证成功。"},
		{"step.completed", "步骤完成", "步骤处理已结束；事件发生不等于验证成功。"},
		{"verification.completed", "验证结束", "验证处理已结束；事件发生不等于验证成功。"},
		{"review.completed", "审查结束", "审查处理已结束；事件发生不等于验证成功。"},
		{"delivery.ready", "交付就绪", "交付已准备；事件发生不等于验证成功。"},
		{"delivery.exported", "交付导出", "交付已导出；事件发生不等于验证成功。"},
		{"mission.blocked", "任务阻塞", "任务已阻塞；事件发生不等于验证成功。"},
		{"mission.paused", "任务暂停", "任务已暂停；事件发生不等于验证成功。"},
		{"mission.interrupted", "任务中断", "任务已中断；事件发生不等于验证成功。"},
	}
}

type HookRule struct {
	ID                string   `json:"id"`
	Revision          int64    `json:"revision"`
	Name              string   `json:"name"`
	Enabled           bool     `json:"enabled"`
	EventKinds        []string `json:"event_kinds"`
	MissionID         string   `json:"mission_id"`
	Action            string   `json:"action"`
	EffectiveAfterSeq int64    `json:"effective_after_seq"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
}

type HookRecord struct {
	ID              int64  `json:"id"`
	EventSeq        int64  `json:"event_seq"`
	MissionID       string `json:"mission_id"`
	MissionRevision int64  `json:"mission_revision"`
	EventKind       string `json:"event_kind"`
	EventCreatedAt  string `json:"event_created_at"`
	RuleID          string `json:"rule_id"`
	RuleRevision    int64  `json:"rule_revision"`
	RuleName        string `json:"rule_name"`
	Action          string `json:"action"`
	HandlerVersion  string `json:"handler_version"`
	Status          string `json:"status"`
	ProcessedAt     string `json:"processed_at"`
}

type HookProcessorState struct {
	InitializedAfterSeq int64  `json:"initialized_after_seq"`
	Cursor              int64  `json:"cursor"`
	LatestEventSeq      int64  `json:"latest_event_seq"`
	PendingEvents       int64  `json:"pending_events"`
	LastProcessedAt     string `json:"last_processed_at"`
}

type HookRecordPage struct {
	Records    []HookRecord `json:"records"`
	NextBefore int64        `json:"next_before"`
}
