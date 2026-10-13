import { SessionMainContent } from "./SessionMainContent";
import type { SessionPaneProps } from "./sessionPaneTypes";
export type { SessionPaneProps } from "./sessionPaneTypes";

export function SessionPane({ sessionView, showSessionPane, ...props }: SessionPaneProps) {

  return (
    <>
    {
              <div
                data-onboarding="workspace"
                style={{
                  width: "100%",
                  flex: 1,
                  minHeight: 0,
                  minWidth: 0,
                  display: "flex",
                  flexDirection: "column",
                  position: "relative",
                }}
              >
                <div
                  style={{
                    flex: 1,
                    minHeight: 0,
                    minWidth: 0,
                    overflow: "hidden",
                    display: "flex",
                    flexDirection: "column",
                  }}
                >
                  <div
                    style={{
                      display: showSessionPane ? "flex" : "none",
                      flex: 1,
                      minHeight: 0,
                      minWidth: 0,
                    }}
                  >
                    {sessionView}
                  </div>
                  <div
                    style={{
                      display: showSessionPane ? "none" : "flex",
                      flex: 1,
                      minHeight: 0,
                      minWidth: 0,
                      flexDirection: "column",
                    }}
                  >
                    <SessionMainContent {...props} />
                  </div>
                </div>
              </div>
            }
    </>
  );
}
