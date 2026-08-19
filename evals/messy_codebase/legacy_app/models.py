"""Legacy application models definition."""


class UserModel:
    def __init__(self, user_id: int, username: str, email: str):
        self.user_id = user_id
        self.username = username
        self.email = email
        self.is_active = True

    def to_dict(self):
        return {
            "user_id": self.user_id,
            "username": self.username,
            "email": self.email,
            "is_active": self.is_active,
        }


class AccountBillingProfile:
    def __init__(self, account_id: str, billing_tier: str):
        self.account_id = account_id
        self.billing_tier = billing_tier
        self.credit_balance = 0.0
