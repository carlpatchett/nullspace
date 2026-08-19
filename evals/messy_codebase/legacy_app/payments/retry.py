import math
import random
import time


class ExponentialBackoffRetryStrategy:
    """Calculates exponential backoff delays with jitter for HTTP payment requests."""

    def __init__(
        self, base_delay: float = 1.0, max_delay: float = 60.0, max_retries: int = 5
    ):
        self.base_delay = base_delay
        self.max_delay = max_delay
        self.max_retries = max_retries

    def calculate_exponential_backoff(self, attempt: int) -> float:
        delay = self.base_delay * (2.0**attempt)
        jitter = random.uniform(0.0, 0.5 * delay)
        return min(self.max_delay, delay + jitter)
