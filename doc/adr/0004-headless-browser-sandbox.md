# ADR 0004: Headless Browser Sandbox Engine for MCP

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-12

## Context & Problem Statement
Autonomous AI coding agents frequently require web browsing capabilities:
1. Researching online API documentation, library reference manuals, and error messages.
2. Verifying and smoke-testing locally running web applications (e.g., `http://localhost:3000`).
3. Capturing visual screenshots of web pages or UI builds to verify rendering accuracy.

Executing browser automation on the developer's host machine creates severe risks:
- **Host Session & Credential Exfiltration**: Untrusted web pages or prompt injections could access the developer's logged-in sessions (GitHub, AWS, banking, email) via shared host browser storage and cookies.
- **Drive-By File Pollution**: Untrusted downloads or cached blobs can compromise the host filesystem.
- **Context Window Saturation**: Standard `curl` fetches raw HTML containing megabytes of minified JavaScript, CSS stylesheets, and tracking scripts, overflowing LLM context limits while failing to execute client-side single-page applications (React, Vue, Svelte).

## Decision Outcome
Introduce a path-jailed, token-sanitized Headless Browser Sandbox Subsystem to Vessel, adding 3 native MCP tools:

1. `vessel_browse_url`:
   - **Parameters**: `url` (string, required), `wait_seconds` (int, optional, default: 2), `include_links` (bool, optional, default: true).
   - **Behavior**: Navigates to the specified URL via headless Chromium inside the sandbox jail. Renders dynamic DOM, strips `<script>`, `<style>`, `<svg>`, `<noscript>`, and inline media, and returns sanitized readable Markdown text, page title, HTTP status, and token estimate.

2. `vessel_screenshot`:
   - **Parameters**: `url` (string, required), `output_path` (string, optional, default: `.vessel/screenshots/page-<timestamp>.png`), `full_page` (bool, optional, default: false), `width` (int, optional, default: 1280), `height` (int, optional, default: 800).
   - **Behavior**: Renders the URL in headless Chromium and captures a PNG screenshot. Saves the file strictly inside `/workspace` using path-jail resolution. Returns relative path, image dimensions, and byte size.

3. `vessel_extract_page`:
   - **Parameters**: `url` (string, required), `selector` (string, optional).
   - **Behavior**: Extracts structured interactive affordances (clickable links with URLs, headings, form inputs, buttons) as structured JSON, enabling agents to plan subsequent browser interactions without token waste.

## Architecture & Container Support
- **Pre-Baked Image**: `configs/Dockerfile.browser-agent` bundles Debian, Chromium (`chromium`), Xvfb/dummy display drivers, and Python 3.
- **Image Alias**: Add alias `browser` -> `vessel-agent:browser` to runtime image resolution.
- **Ephemeral State**: Headless Chromium executes with `--user-data-dir=/tmp/vessel-browser-...` and `--no-sandbox` inside the container namespace, ensuring zero credential persistence across sessions.

## Security & Jail Invariants
1. **Host Isolation**: The browser runs strictly inside the container network namespace. In `--network none` mode, all outbound web requests are blocked.
2. **Path Jail**: Screenshot output files are verified via `ResolveSecurePath` to guarantee they cannot escape `/workspace`.
3. **Token Scrubber**: All extracted DOM text is scrubbed through `tokenfilter.TruncateOutput` to prevent context explosion.
