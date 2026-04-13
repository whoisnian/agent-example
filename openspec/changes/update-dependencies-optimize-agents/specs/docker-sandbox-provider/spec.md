## MODIFIED Requirements

### Requirement: DockerSandbox implements deepagents BaseSandbox
`DockerSandbox` SHALL extend `deepagents.backends.sandbox.BaseSandbox`, implementing `execute()` via Docker `exec_run` so all deepagents filesystem tools and shell commands route through the container. The implementation SHALL comply with the `BaseSandbox` protocol as defined in `deepagents>=0.5.2`, including any methods renamed or added in the 0.5.x release (e.g., renamed backend methods from PR #1907). If method signatures have changed, the implementation SHALL be updated accordingly.

#### Scenario: execute() runs commands inside the container
- **WHEN** `sandbox.execute(command)` is called with a shell command string
- **THEN** the command is executed inside the Docker container with `/workspace` as the working directory and an `ExecuteResponse` is returned

#### Scenario: Filesystem tools operate on container
- **WHEN** a deepagents agent using `DockerSandbox` as its backend calls `write_file("report.html", content)`
- **THEN** the file `/workspace/report.html` is created inside the Docker container

#### Scenario: Protocol compliance with deepagents 0.5.x
- **WHEN** `DockerSandbox` is used as a backend with `deepagents>=0.5.2`
- **THEN** no `NotImplementedError` or `AttributeError` is raised for any method called by the deepagents framework

### Requirement: Provision a Docker container sandbox
The `DockerSandboxProvider` SHALL create and start a Docker container using the `python:3.14.3-bookworm` image with `/workspace` as the container working directory, and return a `DockerSandbox` instance that wraps it. The provider SHALL NOT use deprecated backend factory patterns.

#### Scenario: Default image and working directory
- **WHEN** `DockerSandboxProvider().create()` is called with no arguments
- **THEN** a running Docker container is started from `python:3.14.3-bookworm` with `working_dir` set to `/workspace` and a `DockerSandbox` wrapping that container is returned

#### Scenario: Docker daemon unavailable
- **WHEN** `DockerSandboxProvider().create()` is called and the Docker daemon is not reachable
- **THEN** a `RuntimeError` is raised with a message indicating Docker is unavailable
