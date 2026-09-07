// @lumeweb/mcpcanvas — generic MCP Apps frontend runtime.
//
// This module is the host-rendered MCP Apps bridge/runtime: connect to the MCP
// host over postMessage, boot sandboxed app bundles, and drive the generic
// "start → poll → done" flow and one-shot deep-link machines. Product screens
// (definitions of tools/ids/copy) live in the consuming product; this package
// ships only the shared runtime and its types.
//
// There is no build step: consumers import `"./src/index.ts"` and bundle the
// TypeScript sources themselves (the ui:// sandbox requires self-contained
// bundles, so the whole ext-apps client is inlined per app bundle).

// Host connection bridge (ext-apps App over postMessage).
export { connectApp, type AppIdentity } from "./connect";

// Boot / mount contract.
export { mountApp, bootApp } from "./boot";
export { boot, entryBoot, type AppBoot } from "./loader";

// Runtime version handshake (reads window.__MCPCANVAS_VERSION__).
export { APP_VERSION } from "./version";

// Shared DOM helpers.
export { StatusClass, byId, setStatus } from "./dom";

// Flow machine (start → poll → done).
export {
  createFlowMachine,
  toolError,
  rejectToError,
  type CallTool,
  type ToolResult,
  type FlowConfig,
  type FlowContext,
  FlowState,
  FLOW_STATES,
  FLOW_PENDING,
  FLOW_TERMINAL,
  isFlowPending,
  isFlowTerminal,
} from "./flow";

// One-shot deep-link machine.
export {
  createLinkMachine,
  renderLink,
  currentLinkState,
  linkToolError,
  type LinkConfig,
  type LinkContext,
  LinkState,
} from "./link";

// App entry types + mount helpers.
export {
  type AppBridge,
  type AppDefinition,
  type MachineCurrent,
  currentFlowState,
  renderFlow,
  type FlowRender,
  runAppEntry,
  type AppEntryOptions,
  type FlowElementIds,
  type FlowCopy,
  type FlowConfigCore,
  mountFlowApp,
  type LinkElementIds,
  type LinkCopy,
  type LinkConfigCore,
  type LinkAppEntry,
  mountLinkApp,
} from "./app-entry";

// Flow app entry data shape.
export type { FlowAppEntry } from "./entries/common";
