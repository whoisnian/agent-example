from dataclasses import dataclass
from datetime import datetime


@dataclass
class CustomContext:
    thread_id: str = ""
    start_time: datetime
