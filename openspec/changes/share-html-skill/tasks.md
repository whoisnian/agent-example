## 1. Create Share-HTML Skill File

- [x] 1.1 Create `skills/share-html/SKILL.md` with YAML frontmatter (`name: share-html`, `description`) and markdown instructions telling the agent to use the `execute` tool with the curl upload command
- [x] 1.2 Verify the `SKILL.md` includes the shell command: `FILE_NAME="$(date +%Y%m%d.%H%M%S).html" && curl -s -d @/workspace/report.html "https://share.whoisnian.com:8020/api/file/workspace/${FILE_NAME}" && echo "File uploaded successfully: https://share.whoisnian.com:8020/view/workspace/${FILE_NAME}" || echo "Failed to upload file."`

## 2. Update main.py to Upload Skill and Wire It

- [x] 2.1 In `main.py`, before `create_deep_agent()`, read `skills/share-html/SKILL.md` and call `sandbox.upload_files([("skills/share-html/SKILL.md", content)])` to make the skill available in the sandbox
- [x] 2.2 Add `skills=["/workspace/skills/"]` to the `create_deep_agent()` call
- [x] 2.3 Update `_SYSTEM_PROMPT` to add a step directing the agent to use the `share-html` skill after the html-report subagent completes (reference skill by name only, no implementation details)
- [x] 2.4 Ensure the existing step that reports the file path back to the user remains, and the new step also surfaces the shareable URL

## 3. Update Main Agent Spec

- [x] 3.1 Open `openspec/specs/main-agent/spec.md` and apply the MODIFIED requirement for "Accept research topic and run pipeline" (pipeline now includes skill upload and share-html)
- [x] 3.2 Add the new "Upload share-html skill to sandbox before agent creation" requirement
- [x] 3.3 Add the new "Wire share-html skill into main agent via skills parameter" requirement
- [x] 3.4 Add the new "System prompt instructs agent to share the report" requirement

## 4. Add Share-HTML-Skill Spec

- [x] 4.1 Create `openspec/specs/share-html-skill/spec.md` with the new capability spec (SKILL.md structure, execute tool usage, success/failure scenarios, file name uniqueness)

## 5. Verification

- [x] 5.1 Run the agent end-to-end with a test topic and confirm the share-html skill is loaded and the curl command is executed inside the sandbox
- [x] 5.2 Confirm the agent's final response includes the shareable URL (`https://share.whoisnian.com:8020/view/workspace/<filename>.html`)
- [x] 5.3 Verify that a network failure causes the fallback message to be printed without crashing the pipeline
