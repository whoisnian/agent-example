import os

from dotenv import load_dotenv
from langchain_community.chat_models import ChatTongyi

load_dotenv()

def get_model() -> ChatTongyi:
    """Helper function to create a ChatTongyi model instance with API key from environment variable."""
    api_key = os.environ.get("DASHSCOPE_API_KEY")
    if not api_key:
        raise ValueError(
            "DASHSCOPE_API_KEY is not set. "
            "Copy .env.example to .env and add your DashScope API key."
        )
    return ChatTongyi(model_name="deepseek-v3.2", api_key=api_key)

def truncate_str(s: str, max_len: int = 200) -> str:
    """Truncate a string to a maximum length, adding an ellipsis if it was truncated."""
    if len(s) > max_len:
        return s[:max_len].replace('\n', ' ') + f'... [{len(s)} truncated]'
    return s.strip()

def format_todos(todos: list) -> str:
    """Format a list of todo items into a markdown string with checkboxes."""
    result = ""
    for todo in todos:
        if todo['status'] == 'completed':
            result += f"- [x] {todo['content']}\n"
        elif todo['status'] == 'in_progress':
            result += f"- [-] {todo['content']}\n"
        else:
            result += f"- [ ] {todo['content']}\n"
    return result
