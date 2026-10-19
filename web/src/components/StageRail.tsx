const stages = [
  ["brief", "需求"],
  ["plan", "计划"],
  ["build", "构建"],
  ["verify", "验证"],
  ["deliver", "交付"],
] as const;
export function StageRail({ stage }: { stage?: string }) {
  const displayStage = [
    "research_turn",
    "awaiting_tool_approval",
    "mcp_authorized_pending",
    "mcp_call",
    "mcp_observation_ready",
    "planner_ready",
    "awaiting_approval",
  ].includes(stage ?? "")
    ? "plan"
    : stage;
  const current = stages.findIndex(([key]) => key === displayStage);
  return (
    <ol className="stage-rail" aria-label="任务阶段">
      {stages.map(([key, name], index) => (
        <li
          key={key}
          aria-current={key === displayStage ? "step" : undefined}
          className={index < current ? "completed" : ""}
        >
          <span className="stage-number">
            {index < current ? "✓" : index + 1}
          </span>
          <span>{name}</span>
        </li>
      ))}
    </ol>
  );
}
