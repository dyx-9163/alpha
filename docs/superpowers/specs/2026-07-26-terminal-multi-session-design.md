# Terminal Multi-Session and Split-View Design

## Goal

Extend the SSH Terminal page so an operator can keep multiple independent SSH sessions connected, switch to other panel modules without disconnecting them, and interact with up to four terminals in one split workspace.

## Confirmed User Experience

- Multiple connection tabs may exist at the same time.
- The same server may be opened in multiple independent sessions.
- Up to four sessions may be visible in the split workspace; other sessions remain connected in the background.
- The chosen layout is a top tab bar with an automatic split grid.
- One visible session uses the full workspace, two use left/right columns, and three or four use a 2-by-2 grid.
- On narrow screens, visible sessions stack vertically and the page scrolls naturally.
- Switching the side menu preserves every terminal connection, tab, scrollback buffer, focus, and split selection.
- Refreshing the page, closing the browser, logging out, or truly unmounting the application disconnects and destroys all sessions.

## Current Limitation

`TerminalView.vue` owns one `Terminal`, one `WebSocket`, one connection state, and one terminal DOM element. Connecting again disposes the previous terminal and closes the previous socket. Its `onBeforeUnmount` cleanup also closes the connection whenever the route component is removed. The private router outlet in `App.vue` does not currently cache the Terminal route.

## Chosen Architecture

### Cached terminal workspace

The private router outlet will cache only `TerminalView` with Vue `KeepAlive`. Navigating to another menu deactivates the view instead of unmounting it. Returning to Terminal reactivates the same view and triggers terminal size recalculation.

The public login route remains outside this cache. Logging out removes the private layout and its cache, which guarantees that all terminal sessions are destroyed.

### Terminal coordinator

`TerminalView.vue` becomes a coordinator for serializable session metadata and workspace selection. It owns:

- the configured server list and selected server for creating a connection;
- ordered terminal tab metadata;
- the focused session ID;
- the ordered visible session IDs, limited to four;
- toolbar actions for creating, focusing, splitting, disconnecting, reconnecting, and closing sessions.

It does not directly own xterm or WebSocket instances.

### Independent session pane

A focused `TerminalSessionPane.vue` component owns one session's runtime resources:

- one xterm instance with 10,000 scrollback rows;
- one authenticated terminal WebSocket using the existing endpoint and subprotocols;
- one connection state: `connecting`, `connected`, `disconnected`, or `error`;
- one `ResizeObserver` and the existing terminal grid calculation;
- the terminal input subscription and WebSocket message handlers.

All session pane components remain mounted while `TerminalView` is alive. Sessions outside the visible split set are hidden rather than removed, so their sockets and terminal buffers continue receiving output. When a hidden pane becomes visible or the cached route is reactivated, it recalculates its terminal dimensions.

xterm, WebSocket, and observer objects remain component-local and are not stored in Pinia. This keeps lifecycle ownership explicit and avoids non-serializable global state.

## Interaction Model

### Creating sessions

The operator selects a server and chooses New Connection. The page immediately creates and connects a new tab. Creating another tab for the same server is valid. Tabs use the server name plus a per-server session sequence so duplicate sessions remain distinguishable.

A new session takes the focused split slot. The previously focused session remains connected in the background. If no session is visible, the new session becomes the first full-workspace pane.

### Tabs, focus, and split selection

Each tab displays its label, connection status, split action, and close action.

- Selecting a visible tab focuses its pane and keyboard input.
- Selecting a background tab replaces the current focused split slot without changing the other visible slots. The replaced session moves to the background without disconnecting.
- Add to Split inserts a background session into the visible set when fewer than four panes are visible.
- Remove from Split hides the session without disconnecting it.
- Attempting to add a fifth pane is rejected with a localized maximum-four message.
- Disconnect and Reconnect operate only on the focused session.

The focused pane receives a clear semantic primary border. Background and visible states are distinct from connection status: a connected session may be either visible or hidden.

### Closing sessions

Closing a connected or connecting tab requires confirmation because it terminates an SSH session. Closing a disconnected or failed tab does not require confirmation. Confirmed close removes the session from the tab list and visible set, closes its socket, disconnects its observers, and disposes xterm.

If the closed session was focused, focus moves to another remaining visible session. The grid shrinks naturally; it does not automatically promote an unrelated background session.

## Lifecycle and Data Flow

1. `TerminalView` creates session metadata with a unique client session ID and server reference.
2. `TerminalSessionPane` mounts, creates xterm, opens the authenticated WebSocket, and emits status changes to the coordinator.
3. Incoming socket data writes only to that pane's xterm instance, including while the pane is hidden.
4. Tab and split actions update coordinator metadata without recreating the pane.
5. Side-menu navigation deactivates the cached workspace. No session cleanup runs.
6. Returning to Terminal reactivates the workspace and refits every visible pane.
7. Closing a tab or destroying the private layout performs deterministic per-pane cleanup.

This change reuses the existing `/api/v2/servers/{id}/terminal/ws` contract. It requires no backend API, database, permission, or WebSocket protocol change.

## Error Handling

- A connection error affects only its own tab and pane.
- A background failure is visible through a red tab status indicator; focusing the tab shows the terminal's localized diagnostic message.
- Network disconnect preserves the tab and terminal history. Reconnection is manual so an interrupted interactive command is never silently resumed in a new SSH session.
- Socket callbacks and asynchronous Blob conversion must verify the session is still live before writing or emitting state, preventing stale callbacks from mutating a closed tab.
- Reconnect closes any prior socket for that pane before creating the replacement connection.
- Exceeding four visible panes changes no connection state and only shows a localized warning.
- Logout and true unmount close all sockets and dispose every observer and xterm instance.

## Frontend Structure

Expected focused changes:

- `web/src/App.vue`: cache only the Terminal route in the private router outlet.
- `web/src/views/TerminalView.vue`: terminal workspace coordinator, toolbar, tabs, and split grid.
- `web/src/terminal/TerminalSessionPane.vue`: xterm and WebSocket lifecycle for one session.
- `web/src/terminal/sessions.ts`: pure session selection, focus, split-limit, and close transitions.
- `web/src/terminal/grid.ts`: retain the existing terminal row and column calculation.
- `web/src/i18n/messages.ts`: Chinese and English session, split, reconnect, limit, and close-confirmation text.

No Pinia terminal store is introduced.

## Testing

Implementation will proceed test-first.

### Pure session tests

- Multiple sessions may reference the same server.
- A new session replaces the focused slot and backgrounds the previous session.
- Adding split panes stops at four without changing connection state.
- Removing a pane from the split leaves the session alive.
- Selecting a background tab replaces only the focused slot.
- Closing a visible or focused session repairs visible selection and focus deterministically.

### Session pane tests

- Each pane creates an independent socket and xterm instance.
- Incoming output is routed only to the matching terminal.
- A hidden pane continues receiving output.
- Reactivation and visibility changes refit the terminal.
- Disconnect, reconnect, and dispose close the expected socket exactly once.
- Late socket and Blob callbacks do not write after disposal.

### Route and UI contract tests

- Only `TerminalView` is cached by `KeepAlive`.
- Menu navigation does not unmount terminal panes.
- Logout destroys the cached terminal workspace.
- Tabs expose connection and split states with localized labels.
- The workspace renders full, two-column, and 2-by-2 layouts plus narrow-screen stacking.
- Existing scrollback, viewport overflow, and line-height-aware sizing contracts remain intact.

### Verification

Run focused Vitest files during red-green development, then run:

- `pnpm test:web`
- `pnpm web:build`
- `git diff --check`

## Non-Goals

- Persisting or restoring SSH sessions after page refresh or browser restart.
- Automatic reconnect after network failure.
- Broadcasting one command to multiple terminals.
- Saving named terminal workspaces or split layouts.
- Changing terminal authentication, RBAC, backend APIs, or WebSocket protocol.
- Moving xterm or WebSocket instances into global Pinia state.
