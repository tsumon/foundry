import { useState, type ComponentProps } from "react";
import { CreateMission } from "./CreateMission";
import { IdeaCreate } from "./IdeaCreate";
import type { Mission } from "../types";

export function CreateEntry(
  props: ComponentProps<typeof CreateMission> & {
    continueFrom?: Mission;
    onOpenSettings?: () => void;
  },
) {
  const [entry, setEntry] = useState<"idea" | "repository">(
    props.initialRepository && !props.continueFrom ? "repository" : "idea",
  );
  const [visitedRepository, setVisitedRepository] = useState(
    !!props.initialRepository && !props.continueFrom,
  );
  const [busy, setBusy] = useState(false);
  const changed = (value: boolean) => {
    setBusy(value);
    props.onBusyChange?.(value);
  };
  const currentEntry = props.continueFrom ? "idea" : entry;
  return (
    <>
      <div className="creation-entry" role="tablist" aria-label="创建方式">
        <button
          role="tab"
          aria-selected={currentEntry === "idea"}
          disabled={busy}
          onClick={() => setEntry("idea")}
        >
          {props.continueFrom ? "继续改进应用" : "从想法创建"}
        </button>
        {!props.continueFrom && (
          <button
            role="tab"
            aria-selected={currentEntry === "repository"}
            disabled={busy}
            onClick={() => {
              setVisitedRepository(true);
              setEntry("repository");
            }}
          >
            改进已有仓库
          </button>
        )}
      </div>
      <div hidden={currentEntry !== "idea"}>
        <IdeaCreate
          {...props}
          active={props.active !== false && currentEntry === "idea"}
          onBusyChange={changed}
        />
      </div>
      {!props.continueFrom && visitedRepository && (
        <div hidden={currentEntry !== "repository"}>
          <CreateMission
            {...props}
            active={props.active !== false && currentEntry === "repository"}
            onBusyChange={changed}
          />
        </div>
      )}
    </>
  );
}
