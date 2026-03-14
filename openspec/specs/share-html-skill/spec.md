# share-html-skill Specification

## Purpose
A DeepAgents skill that uploads `/workspace/report.html` from the sandbox to a file-sharing service using `curl` and returns a shareable URL to the user.

## Requirements

### Requirement: Share-html skill is a DeepAgents SKILL.md file
The share-html capability SHALL be implemented as a DeepAgents skill: a `SKILL.md` file with YAML frontmatter (name, description) and markdown instructions that tell the agent how to upload `/workspace/report.html` to `https://share.whoisnian.com:8020` using `curl` via the `execute` tool.

#### Scenario: SKILL.md structure
- **WHEN** the `SKILL.md` is parsed by `SkillsMiddleware`
- **THEN** it has a valid YAML frontmatter block with `name: share-html` and a non-empty `description`, and the body contains the curl command to execute

#### Scenario: Skill instructs use of execute tool
- **WHEN** the main agent reads the share-html skill
- **THEN** the skill instructs the agent to use the `execute` tool with the shell command: `FILE_NAME="$(date +%Y%m%d.%H%M%S).html" && curl -s -d @/workspace/report.html "https://share.whoisnian.com:8020/api/file/workspace/${FILE_NAME}" && echo "File uploaded successfully: https://share.whoisnian.com:8020/view/workspace/${FILE_NAME}" || echo "Failed to upload file."`

#### Scenario: Successful upload
- **WHEN** the main agent follows the share-html skill and executes the curl command
- **THEN** `report.html` is uploaded to the sharing service and the agent includes the shareable URL `https://share.whoisnian.com:8020/view/workspace/<filename>.html` in its final response

#### Scenario: Upload failure (network error)
- **WHEN** the execute tool runs the curl command and the service is unreachable
- **THEN** the shell fallback `|| echo "Failed to upload file."` message is returned to the agent, the agent reports the failure to the user, and the pipeline does not crash

#### Scenario: File name uniqueness
- **WHEN** the upload command is executed
- **THEN** the uploaded file is named using the current timestamp (`date +%Y%m%d.%H%M%S`) to avoid collisions on the sharing server
