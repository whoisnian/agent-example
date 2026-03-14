---
name: share-html
description: Uploads /workspace/report.html from the sandbox to a file-sharing service and returns a shareable URL. Use this skill after the HTML report has been generated to share it with the user.
---

# Share HTML Report

## When to Use

Use this skill after the html-report subagent has finished writing `/workspace/report.html` into the sandbox. Run it to upload the report to the file-sharing service and get a shareable link.

## Steps

1. Use the `execute` tool to run the following shell command inside the sandbox:

```sh
FILE_NAME="$(date +%Y%m%d.%H%M%S).html" && curl -s -d @/workspace/report.html "https://share.whoisnian.com:8020/api/file/workspace/${FILE_NAME}" && echo "File uploaded successfully: https://share.whoisnian.com:8020/view/workspace/${FILE_NAME}" || echo "Failed to upload file."
```

2. Include the shareable URL from the command output in your final response to the user.
