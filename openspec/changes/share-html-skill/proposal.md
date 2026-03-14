## Why

After the agent pipeline generates an HTML research report, it currently only saves it locally. Users have no easy way to share the report with others. Adding an automatic upload step to a file-sharing service gives users an immediately shareable URL without any manual action.

## What Changes

- Add a `share-html` DeepAgents skill: a `SKILL.md` file that instructs the main agent how to upload `/workspace/report.html` to a file-sharing service using `curl` inside the sandbox.
- Upload the `SKILL.md` file to the sandbox before starting the pipeline and wire `skills=["/workspace/skills/"]` into `create_deep_agent()` so the main agent reads the skill at runtime.
- Update the main agent's system prompt to add a step directing it to share the HTML report after generating it.
- Update the main-agent spec with requirements covering the share-html skill integration.

## Capabilities

### New Capabilities
- `share-html-skill`: A DeepAgents `SKILL.md`-based skill that the main agent reads from the sandbox at runtime. The skill instructs the agent to use the `execute` tool to run a `curl` command that uploads `/workspace/report.html` to `https://share.whoisnian.com:8020` and returns a shareable view URL.

### Modified Capabilities
- `main-agent`: Wired with `skills=["/workspace/skills/project/"]`; gets a new pipeline step in the system prompt; a new spec requirement covers the skill integration.

## Impact

- `main.py`: upload the `share-html/SKILL.md` to the sandbox before creating the agent; pass `skills=["/workspace/skills/project/"]` to `create_deep_agent()`; update `_SYSTEM_PROMPT` to include a share step.
- New file `skills/share-html/SKILL.md` containing the skill definition.
- `openspec/specs/main-agent/spec.md`: New requirements added.
- No new Python dependencies; `curl` is expected to be available inside the Docker sandbox image.
- The file-sharing endpoint `https://share.whoisnian.com:8020` must be reachable from within the sandbox container.
