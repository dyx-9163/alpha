# AIFAR Training Blank Slides Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the seven blank slides in the 58-slide AIFAR training deck with concise English operational guidance while preserving the source presentation's style and all nonblank slides.

**Architecture:** Treat the supplied PPTX as the only visual template. Build a starter deck by mapping every output slide to a source slide, remapping only the seven blank positions to existing four-step or four-card slides, then edit inherited text objects through `@oai/artifact-tool` and export a separate PPTX copy.

**Tech Stack:** PowerPoint `.pptx`, `@oai/artifact-tool` JavaScript ES modules, bundled presentation template-following scripts, PowerPoint XML checks, slide PNG/layout export.

## Global Constraints

- Preserve the original 58-slide order and all nonblank slides.
- Replace only slides 29, 31, 33, 35, 37, 39, and 41.
- Keep all new audience-facing copy in English.
- Reuse inherited elements and preserve typography, spacing, logos, background, accent colors, and master/layout relationships.
- Do not invent screenshots, commands, credentials, environment states, customer-specific values, or external claims.
- Shorten copy before changing font size.
- Export a separate final file at `D:/workspace/aifar-deployment/deliverables/Aifar Training -7.27(1)-completed.pptx`.

---

### Task 1: Build the complete template mapping and edit contract

**Files:**
- Create: `tmp/pptx-fill-20260728/template-audit.txt`
- Create: `tmp/pptx-fill-20260728/template-frame-map.json`
- Create: `tmp/pptx-fill-20260728/deviation-log.txt`
- Create: `tmp/pptx-fill-20260728/source-notes.txt`

**Interfaces:**
- Consumes: `tmp/pptx-fill-20260728/source.pptx`, `template-inspect/template-inspect.ndjson`, source slide PNGs, and source slide layout JSON.
- Produces: a validated `template-frame-map.json` accepted by `prepare_template_starter_deck.mjs`.

- [ ] **Step 1: Record the source-deck audit**

Write that the source contains 58 slides, uses a white background with blue/orange accents and three inherited brand images, and has true blank slides at 29, 31, 33, 35, 37, 39, and 41. Record slide 30 as the four-step source pattern and slide 34 as the four-card source pattern.

- [ ] **Step 2: Create the 58-slide frame map**

Map every nonblank output slide to the same-numbered source slide. Use these exceptions:

```json
{
  "29": 30,
  "31": 34,
  "33": 30,
  "35": 34,
  "37": 30,
  "39": 30,
  "41": 30
}
```

For slides mapped from source slide 30, classify these inherited text elements as `rewrite`: `sh/apg36do7`, `sh/6twju9o3`, `sh/2l8f6p0r`, `sh/3mhwzu1c`, `sh/ba1w3uh8`, `sh/sf6d0fip`, `sh/7y5kf2pw`, `sh/mxw36xob`, `sh/u1g3aho7`, and `sh/5srmpw7u`.

For slides mapped from source slide 34, classify these inherited text elements as `rewrite`: `sh/qx8napc7`, `sh/y18ne9c3`, `sh/e90vipcz`, `sh/za9wbutk`, `sh/mxkvmpcb`, `sh/nytwfutg`, `sh/l87mpkzq`, `sh/k7y5gfi5`, `sh/tc7mtkzm`, and `sh/8by5kfi1`.

All other inherited elements use `keep`. Record the seven original blank source slides as omitted because they contain no usable inherited content slots.

- [ ] **Step 3: Record deviations and provenance**

In `deviation-log.txt`, record only the seven intentional blank-slide substitutions. In `source-notes.txt`, state that all copy is synthesized from surrounding slides and the approved design, with no external claims or assets.

- [ ] **Step 4: Validate and prepare the starter deck**

Run:

```powershell
node "$SKILL_DIR/template_following_scripts/prepare_template_starter_deck.mjs" --workspace "$TMP_DIR" --pptx "$TMP_DIR/source.pptx" --map "$TMP_DIR/template-frame-map.json" --out "$TMP_DIR/template-starter.pptx" --preview-dir "$TMP_DIR/template-starter-preview" --layout-dir "$TMP_DIR/template-starter-layout" --contact-sheet "$TMP_DIR/template-starter-contact-sheet.png"
```

Expected: exit code 0, 58 starter slides, and no mapping or unresolved-target errors.

### Task 2: Edit inherited text and export the completed deck

**Files:**
- Create: `tmp/pptx-fill-20260728/complete_blank_slides.mjs`
- Create: `deliverables/Aifar Training -7.27(1)-completed.pptx`

**Interfaces:**
- Consumes: `tmp/pptx-fill-20260728/template-starter.pptx` and the mapped slide positions.
- Produces: a 58-slide final PPTX with seven completed operational slides.

- [ ] **Step 1: Implement a focused imported-deck editor**

Import `template-starter.pptx` with `PresentationFile.importPptx`. For each target slide, resolve the inherited shapes by slide-scoped names (`page-title`, `page-subtitle`, `step-1-title` through `step-4-body`, or `card-1-title` through `card-4-body`), replace text only, preserve text style and geometry, then export through `PresentationFile.exportPptx`.

- [ ] **Step 2: Populate slide 29**

```text
Title: Daily Operations Workflow
Subtitle: Use a consistent operating cycle to reduce service risk and keep actions traceable.
01 Open Platform
• Sign in with assigned account
• Select the target service
• Confirm current status
02 Review Health
• Check active alarms
• Review resource usage
• Confirm dependencies
03 Perform Operation
• Use the approved action
• Monitor task progress
• Avoid parallel changes
04 Verify and Record
• Recheck service health
• Validate user access
• Record results and exceptions
```

- [ ] **Step 3: Populate slide 31**

```text
Title: Start and Stop Verification Checklist
Subtitle: Complete these checks before and after planned service startup or shutdown.
Before Startup
• Confirm the approved change window
• Check dependency availability
• Record the configuration baseline
After Startup
• Confirm required services are running
• Test login, messaging, and file transfer
• Check for critical alarms
Before Shutdown
• Notify affected users
• Review active tasks and traffic
• Confirm rollback readiness
After Shutdown
• Confirm processes have stopped
• Verify dependent components are safe
• Record and hand over the result
```

- [ ] **Step 4: Populate slide 33**

```text
Title: Global Configuration Change Workflow
Subtitle: Apply shared configuration changes through a controlled and verifiable sequence.
01 Identify Scope
• Confirm the target component
• Review affected services
• Define the validation method
02 Record Baseline
• Export current values
• Record the change owner
• Prepare rollback information
03 Change and Reload
• Update approved parameters
• Reload only affected services
• Monitor the operation result
04 Validate and Document
• Run functional checks
• Confirm configuration consistency
• Record the final state
```

- [ ] **Step 5: Populate slide 35**

```text
Title: Enterprise Communication Validation
Subtitle: Validate each external integration after configuration changes and before service handover.
Time Synchronization
• Confirm the configured NTP source
• Compare system time across nodes
• Record synchronization status
SIEM Forwarding
• Send a controlled test event
• Confirm receipt and field mapping
• Check forwarding failures
Directory Synchronization
• Test organization and user updates
• Confirm synchronization scope
• Review rejected records
Mail Notification
• Send a test notification
• Confirm sender and recipient settings
• Review delivery errors
```

- [ ] **Step 6: Populate slide 37**

```text
Title: Data and Storage Maintenance Workflow
Subtitle: Protect availability by reviewing capacity, safeguarding data, and validating every maintenance action.
01 Review Capacity
• Check database and object storage use
• Review growth trends
• Confirm alarm thresholds
02 Protect Data
• Verify the latest backup
• Confirm retention requirements
• Record the maintenance scope
03 Perform Maintenance
• Use approved cleanup actions
• Monitor running tasks
• Avoid deleting active data
04 Validate Health
• Recheck available capacity
• Test file access
• Record results and exceptions
```

- [ ] **Step 7: Populate slide 39**

```text
Title: Log Troubleshooting Workflow
Subtitle: Narrow the time window, correlate evidence, and preserve an auditable troubleshooting record.
01 Define the Window
• Record symptom and occurrence time
• Confirm server time zone
• Identify affected services
02 Collect Evidence
• Gather service and system logs
• Include audit and integration logs
• Preserve original timestamps
03 Correlate Findings
• Search stable error keywords
• Compare events across components
• Confirm the first failure point
04 Resolve and Archive
• Apply the approved correction
• Retest the affected function
• Archive evidence and outcome
```

- [ ] **Step 8: Populate slide 41**

```text
Title: Alarm Response and Health Check
Subtitle: Use alarms as the starting point for impact assessment, safe action, and verified closure.
01 Confirm the Alarm
• Verify source and timestamp
• Check whether it is still active
• Remove duplicate notifications
02 Assess Impact
• Identify affected services and users
• Review dependencies and resources
• Assign severity and owner
03 Act Safely
• Follow the approved response path
• Monitor the remediation task
• Escalate when recovery is uncertain
04 Verify and Close
• Confirm service and resource health
• Run a user-facing function check
• Record cause, action, and result
```

- [ ] **Step 9: Export the final PPTX**

Write only the final deck outside the temporary workspace. Expected: `deliverables/Aifar Training -7.27(1)-completed.pptx` exists and contains 58 slides.

### Task 3: Render, inspect, and verify the final artifact

**Files:**
- Create: `tmp/pptx-fill-20260728/final-render/slide-*.png`
- Create: `tmp/pptx-fill-20260728/final-layout/slide-*.layout.json`
- Create: `tmp/pptx-fill-20260728/final-montage.png`
- Create: `tmp/pptx-fill-20260728/final-qa.txt`

**Interfaces:**
- Consumes: the exported final PPTX and starter-deck layout evidence.
- Produces: visual, structural, overflow, and template-fidelity evidence supporting delivery.

- [ ] **Step 1: Render all final slides and layouts**

Use artifact-tool to export all 58 slide PNGs, all 58 layout JSON files, and a montage. Expected: no blank render at slides 29, 31, 33, 35, 37, 39, or 41.

- [ ] **Step 2: Inspect the seven edited slides at full size**

Verify title lines do not wrap, all bullet lines are visible, the blue/orange sequence remains consistent, logos are present, and no object clips or overlaps another object.

- [ ] **Step 3: Run overflow and XML placeholder checks**

Run `slides_test.py` against the final PPTX and inspect every final slide XML part for empty structural placeholders. Expected: no content outside the slide canvas and no unresolved empty placeholders introduced by the seven substitutions.

- [ ] **Step 4: Run template fidelity validation**

Run:

```powershell
node "$SKILL_DIR/template_following_scripts/check_template_fidelity.mjs" --workspace "$TMP_DIR" --starter-pptx "$TMP_DIR/template-starter.pptx" --final-pptx "$FINAL_PPTX" --map "$TMP_DIR/template-frame-map.json" --starter-layout-dir "$TMP_DIR/template-starter-layout" --final-layout-dir "$TMP_DIR/final-layout" --edit-dir "$TMP_DIR"
```

Expected: exit code 0 with only the mapped inherited text changes.

- [ ] **Step 5: Record the final QA result**

Record slide count, target-slide titles, render inspection result, overflow result, placeholder result, fidelity result, and final file SHA-256 in `final-qa.txt`.
