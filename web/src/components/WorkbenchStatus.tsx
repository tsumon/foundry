import { buildMetadata, type BuildMetadata } from "../buildMetadata";

export function WorkbenchStatus({
  loading,
  error,
  snapshotReceived,
  stream,
  lastDataReceivedAt,
  build = buildMetadata,
}: {
  loading: boolean;
  error: string;
  snapshotReceived: boolean;
  stream: string;
  lastDataReceivedAt: number | null;
  build?: BuildMetadata;
}) {
  const taskState = loading
    ? snapshotReceived
      ? "正在刷新（保留最后数据）"
      : "正在读取"
    : error
      ? snapshotReceived
        ? "更新失败（保留最后数据）"
        : "读取失败"
      : snapshotReceived
        ? "已收到"
        : "尚未读取";
  const received = lastDataReceivedAt
    ? new Date(lastDataReceivedAt).toLocaleString()
    : "尚未收到";
  return (
    <section className="workbench-status" aria-label="工作台状态">
      <div className="workbench-status-mobile">
        <span>任务数据：{taskState}</span>
        <span>事件流：{stream}</span>
        <details>
          <summary>前端 {build.version}</summary>
          <dl>
            <div>
              <dt>最近收到任务数据</dt>
              <dd>{received}</dd>
            </div>
            <div>
              <dt>构建时间</dt>
              <dd>{build.timestamp}</dd>
            </div>
          </dl>
        </details>
      </div>
      <dl className="workbench-status-full">
        <div>
          <dt>任务数据</dt>
          <dd>{taskState}</dd>
        </div>
        <div>
          <dt>状态事件流</dt>
          <dd>{stream}</dd>
        </div>
        <div className="status-wide">
          <dt>最近收到任务数据</dt>
          <dd>{received}</dd>
        </div>
        <div>
          <dt>前端版本</dt>
          <dd>{build.version}</dd>
        </div>
        <div className="status-wide">
          <dt>构建时间</dt>
          <dd>{build.timestamp}</dd>
        </div>
      </dl>
    </section>
  );
}
