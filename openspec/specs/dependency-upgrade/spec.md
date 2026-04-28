# dependency-upgrade Specification

## Purpose
Ensures all project dependencies are at their latest compatible versions and that import paths and runtime behavior remain functional after upgrades.

## Requirements

### Requirement: All dependencies at latest compatible versions
The project SHALL declare dependency versions in `pyproject.toml` that resolve to the latest stable releases. Specifically: `deepagents>=0.5.2`, `langchain-openai` (replacing `dashscope` and `ChatTongyi` usage), and all other dependencies (`ddgs`, `docker`, `duckduckgo-search`, `langchain`, `langchain-community`, `langgraph-checkpoint-sqlite`, `python-dotenv`) at their latest compatible versions. The `dashscope` package SHALL be removed from dependencies since the DashScope API is now accessed via OpenAI-compatible mode through `langchain-openai`.

#### Scenario: langchain-openai added
- **WHEN** `uv sync` is run
- **THEN** `langchain-openai` is installed and available for import

#### Scenario: dashscope removed
- **WHEN** `pyproject.toml` is inspected
- **THEN** the `dashscope` package is no longer listed as a dependency

#### Scenario: All dependencies resolve without conflicts
- **WHEN** `uv sync` is run after the dependency changes
- **THEN** all dependencies resolve successfully with no version conflicts

### Requirement: Verify import paths after upgrade
All import paths used in the project SHALL remain valid after the dependency changes. `from langchain_openai import ChatOpenAI` SHALL replace `from langchain_community.chat_models import ChatTongyi`. The `DuckDuckGoSearchRun` import from `langchain_community` SHALL remain unchanged.

#### Scenario: langchain-openai import valid
- **WHEN** `from langchain_openai import ChatOpenAI` is executed
- **THEN** it imports successfully without `ImportError`

#### Scenario: langchain-community DuckDuckGo import still valid
- **WHEN** `from langchain_community.tools import DuckDuckGoSearchRun` is executed
- **THEN** it imports successfully without `ImportError`

#### Scenario: deepagents imports still valid
- **WHEN** `from deepagents.graph import create_deep_agent` is executed
- **THEN** it imports successfully without `ImportError`

### Requirement: No Pydantic V1 blocking errors on Python 3.14
The upgraded dependencies SHALL NOT produce blocking Pydantic V1 errors on Python 3.14. Deprecation warnings are acceptable if they do not prevent execution.

#### Scenario: Pipeline runs despite Pydantic warnings
- **WHEN** the pipeline is executed on Python 3.14
- **THEN** it completes successfully regardless of any Pydantic deprecation warnings
