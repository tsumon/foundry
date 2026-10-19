import { useLayoutEffect, useRef, type RefObject } from "react";
import { act, render } from "@testing-library/react";
import { expect, it } from "vitest";
import { useCapabilityRequest } from "./useCapabilityRequest";
import { deferred } from "./testFixtures";

it.each([true, false])(
  "accepts the first enabled interaction before passive effects (initial enabled: %s)",
  async (initiallyEnabled) => {
    const pending = deferred<void>();
    const accepted: boolean[] = [];
    function Panel({
      enabled,
      button,
    }: {
      enabled: boolean;
      button: RefObject<HTMLButtonElement | null>;
    }) {
      const request = useCapabilityRequest(enabled);
      return (
        <button
          ref={button}
          disabled={!enabled || request.busy}
          onClick={() =>
            void request.run(async (valid) => {
              accepted.push(valid());
              await pending.promise;
            })
          }
        >
          Read record
        </button>
      );
    }
    function Harness({ enabled }: { enabled: boolean }) {
      const button = useRef<HTMLButtonElement>(null);
      useLayoutEffect(() => {
        if (enabled) {
          // Exercise the committed enabled DOM before passive effects flush.
          // An immediate second interaction must still be rejected by the ref lock.
          button.current?.click();
          button.current?.click();
        }
      }, [enabled]);
      return <Panel enabled={enabled} button={button} />;
    }
    const view = render(<Harness enabled={initiallyEnabled} />);
    if (!initiallyEnabled) view.rerender(<Harness enabled />);
    expect(accepted).toEqual([true]);
    await act(async () => pending.resolve());
  },
);
