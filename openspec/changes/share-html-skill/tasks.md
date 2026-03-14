## 1. Create Share-HTML Skill File

- [ ] 1.1 Create `skills/share-html/SKILL.md` with YAML frontmatter (`name: share-html`, `description`) and markdown instructions telling the agent to use the `execute` tool with the curl upload command
- [ ] 1.2 Verify the `SKILL.md` includes the shell command: `FILE_NAME="$(date +%Y%m%d.%H%M%S).html" && curl -s -d @/workspace/report.html "https://share.whoisnian.com:8020/api/file/workspace/${FILE_NAME}" && echo "File uploaded successfully: https://share.whoisnian.com:8020/view/workspace/${FILE_NAME}" || echo "Failed to upload file."`

## 2. Update main.py to Upload Skill and Wire It

- [ ] 2.1 In `main.py`, before `create_deep_agent()`, read `skills/share-html/SKILL.md` and call `sandbox.upload_files([("skills/share-html/SKILL.md", content)])` to make the skill available in the sandbox
- [ ] 2.2 Add `skills=["/workspace/skills/"]` to the `create_deep_agent()` call
- [ ] 2.3 Update `_SYSTEM_PROMPT` to add a step directing the agent to use the `share-html` skill after the html-report subagent completes (reference skill by name only, no implementation details)
- [ ] 2.4 Ensure the existing step that reports the file path back to the user remains, and the new step also surfaces the shareable URL

## 3. Update Main Agent Spec

- [ ] 3.1 Open `openspec/specs/main-agent/spec.md` and apply the MODIFIED requirement for "Accept research topic and run pipeline" (pipeline now includes skill upload and share-html)
- [ ] 3.2 Add the new "Upload share-html skill to sandbox before agent creation" requirement
- [ ] 3.3 Add the new "Wire share-html skill into main agent via skills parameter" requirement
- [ ] 3.4 Add the new "System prompt instructs agent to share the report" requirement

## 4. Add Share-HTML-Skill Spec

- [ ] 4.1 Create `openspec/specs/share-html-skill/spec.md` with the new capability spec (SKILL.md structure, execute tool usage, success/failure scenarios, file name uniqueness)

## 5. Verification

- [ ] 5.1 Run the agent end-to-end with a test topic and confirm the share-html skill is loaded and the curl command is executed inside the sandbox
- [ ] 5.2 Confirm the agent's final response includes the shareable URL (`https://share.whoisnian.com:8020/view/workspace/<filename>.html`)
- [ ] 5.3 Verify that a network failure causes the fallback message to be printed without crashing the pipeline
