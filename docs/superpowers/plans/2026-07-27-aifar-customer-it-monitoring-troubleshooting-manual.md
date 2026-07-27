# AIFAR Customer IT Monitoring and Troubleshooting Manual Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generate a standalone Chinese Word manual for customer IT teams that contains only read-only operational monitoring, troubleshooting evidence collection, and escalation guidance.

**Architecture:** Keep the original training manual unchanged. Author a focused Markdown source, reuse the existing deterministic Word styling helpers and sanitized screenshots through a small wrapper, and validate the resulting DOCX with content, OOXML, accessibility, privacy, and render checks.

**Tech Stack:** Markdown, bundled Python 3, python-docx, OOXML, packaged Documents skill QA scripts.

## Global Constraints

- Source of truth: `docs/superpowers/specs/2026-07-27-aifar-customer-it-monitoring-troubleshooting-manual-design.md`.
- Original file `docs/AIFAR登录后功能培训手册.docx` must remain byte-for-byte unchanged.
- Output file: `docs/AIFAR客户IT运行监控与故障排查手册.docx`.
- Audience is customer IT only; do not include administrator, implementation, deployment, development, or change-execution responsibilities.
- Do not provide steps for stopping or cancelling tasks, restarting or scaling Runtime, changing containers, installing or removing applications, publishing Nacos configuration, changing credentials or settings, submitting server changes, or running SSH commands.
- Screenshots must not expose passwords, private keys, tokens, complete server addresses, complete connection strings, production data, or local absolute workspace paths.
- Use `compact_reference_guide` with the named A4 Chinese-font override already implemented by the existing training-manual builder: A4 portrait, 1.7 cm left/right margins, 1.6 cm top/bottom margins, Microsoft YaHei 10.5 pt body, 1.25 line spacing, true headings, true Word numbering, fixed DXA table geometry, inline images, running header, and page-number footer.
- Use the `editorial_cover` first-page archetype with restrained blue operational styling.
- Execute inline in the current session; do not dispatch subagents.
- Do not push, merge, or stage unrelated worktree changes.

---

### Task 1: Author the focused customer IT source

**Files:**
- Create: `.tmp/aifar-customer-it-monitoring/AIFAR客户IT运行监控与故障排查手册.md`
- Read: `.tmp/aifar-training/AIFAR登录后功能培训手册.md`
- Read: `.tmp/aifar-training/screenshots/*.png`

**Interfaces:**
- Consumes: the approved information architecture and existing sanitized screenshots.
- Produces: UTF-8 Markdown with `#` headings, real list source items, Markdown tables, `[!NOTE]`/`[!WARNING]` callouts, and `[[FIG:file|caption]]` figure markers accepted by the builder.

- [ ] **Step 1: Write the complete manual source**

  Create eight first-level sections: scope and read-only boundary; monitoring overview; daily inspection; status interpretation; troubleshooting workflow; common fault scenarios; evidence and escalation; checklists and quick references. Use screenshots `01-dashboard.png`, `10-tasks.png`, and `11-audit.png` only. Crop the task and audit screenshots inside the DOCX to de-emphasize mutation controls; do not alter the source PNG files.

- [ ] **Step 2: Scan the source for scope violations and placeholders**

  Run:

  ```powershell
  rg -n "T[B]D|T[O]DO|稍后.{0,2}补充|单击.*(停止|重启|删除|扩容|缩容|安装|卸载|发布|回滚)|执行.*命令|BEGIN [A-Z ]*PRIVATE KEY|aifar-session-token|Oversea\.123" -- ".tmp/aifar-customer-it-monitoring/AIFAR客户IT运行监控与故障排查手册.md"
  ```

  Expected: no matches.

### Task 2: Build the standalone DOCX

**Files:**
- Create: `.tmp/aifar-customer-it-monitoring/build_customer_it_manual.py`
- Reuse: `.tmp/aifar-training/build_training_manual.py`
- Create: `docs/AIFAR客户IT运行监控与故障排查手册.docx`

**Interfaces:**
- Consumes: the focused Markdown source and five sanitized screenshots.
- Produces: an A4 DOCX with the approved title, header, footer, metadata, real headings/lists, fixed tables, captions, and image alt text.

- [ ] **Step 1: Create a wrapper around the existing deterministic builder**

  Load `.tmp/aifar-training/build_training_manual.py` with `importlib.util`, override `SOURCE`, `SCREENSHOTS`, `OUTPUT`, `add_header_footer`, and `add_cover`, call `build()`, then reopen the result and set title, subject, author, last-modified-by, and keywords for the customer IT manual.

- [ ] **Step 2: Generate the Word document with the bundled Python runtime**

  Run:

  ```powershell
  C:\Users\Administrator\.cache\codex-runtimes\codex-primary-runtime\dependencies\python\python.exe ".tmp/aifar-customer-it-monitoring/build_customer_it_manual.py" "docs/AIFAR客户IT运行监控与故障排查手册.docx"
  ```

  Expected: the output path is printed and the DOCX is non-empty.

### Task 3: Add focused structural and privacy validation

**Files:**
- Create: `.tmp/aifar-customer-it-monitoring/validate_customer_it_manual.py`
- Test: `docs/AIFAR客户IT运行监控与故障排查手册.docx`

**Interfaces:**
- Consumes: generated DOCX.
- Produces: exit code 0 plus paragraph, heading, table, image, section, and byte counts; otherwise a precise list of failures.

- [ ] **Step 1: Implement deterministic validation**

  Require all eight first-level sections and core phrases for read-only monitoring, evidence collection, data freshness, task-step/target/log review, and escalation. Reject placeholders, secrets, local absolute paths, complete IPv4 addresses, change-execution phrases, fake bullets, non-A4 sections, missing PAGE field, non-inline images, missing image alt text, missing numbering definitions, and inconsistent table geometry.

- [ ] **Step 2: Run focused validation**

  Run:

  ```powershell
  C:\Users\Administrator\.cache\codex-runtimes\codex-primary-runtime\dependencies\python\python.exe ".tmp/aifar-customer-it-monitoring/validate_customer_it_manual.py" "docs/AIFAR客户IT运行监控与故障排查手册.docx"
  ```

  Expected: `PASS`, eight Heading 1 paragraphs, three inline images, A4 sections, and no forbidden patterns.

### Task 4: Run document QA and preserve the original

**Files:**
- Verify: `docs/AIFAR客户IT运行监控与故障排查手册.docx`
- Verify unchanged: `docs/AIFAR登录后功能培训手册.docx`
- QA output: `.tmp/aifar-customer-it-monitoring/rendered/`

**Interfaces:**
- Consumes: final DOCX and pre-build SHA-256 of the original training manual.
- Produces: fresh verification evidence or an explicit LibreOffice/Word rendering limitation.

- [ ] **Step 1: Run accessibility and OOXML audits**

  Run the packaged accessibility audit, heading/image/section audits where available, and the focused validator. Confirm image descriptions and table header flags are present.

- [ ] **Step 2: Attempt full render to page PNGs**

  Run:

  ```powershell
  C:\Users\Administrator\.cache\codex-runtimes\codex-primary-runtime\dependencies\python\python.exe "C:\Users\Administrator\.codex\plugins\cache\openai-primary-runtime\documents\26.723.12215\skills\documents\render_docx.py" "docs/AIFAR客户IT运行监控与故障排查手册.docx" --output_dir ".tmp/aifar-customer-it-monitoring/rendered" --emit_pdf
  ```

  Expected when LibreOffice is installed: one PNG per page and a non-empty PDF, followed by inspection of every page. If the renderer reports missing `soffice`, record the limitation and rely only on the structural audits without claiming visual render success.

- [ ] **Step 3: Verify the original file is unchanged and final file is isolated**

  Compare the original manual SHA-256 captured before generation with the final SHA-256. Inspect `git status --short` and ensure unrelated files were not staged or overwritten.

### Task 5: Record the reusable project conclusion and hand off

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: final validation evidence.
- Produces: one concise problem/conclusion entry without secrets or long logs.

- [ ] **Step 1: Append the final document result and verification status**

  Record the output path, the read-only customer IT scope, screenshot count, structural validation result, and whether page rendering was available.

- [ ] **Step 2: Deliver only the requested DOCX**

  Cite the final DOCX exactly once using the Documents skill output citation. Mention any render limitation plainly; do not link intermediate Markdown, scripts, render images, or PDF.
