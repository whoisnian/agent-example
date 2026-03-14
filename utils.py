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
