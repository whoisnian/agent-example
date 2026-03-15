from dataclasses import dataclass
from datetime import datetime


@dataclass
class CustomContext:
    start_time: datetime
