"""Notification delivery schedule."""


class NotificationDeliveryBackoff:
    def __init__(self, retry_limit: int = 3):
        self.retry_limit = retry_limit

    def schedule_push_notification(self, device_token: str, message: str) -> bool:
        print(f"Sending push notification to {device_token}: {message}")
        return True
