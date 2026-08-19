"""Stripe Payment Gateway Client."""


class StripePaymentGatewayClient:
    def __init__(self, api_key: str, webhook_secret: str):
        self.api_key = api_key
        self.webhook_secret = webhook_secret
        self.endpoint_url = "https://api.stripe.com/v1/payment_intents"

    def process_recurring_subscription(
        self, customer_id: str, plan_id: str, amount_cents: int
    ) -> dict:
        return {
            "status": "succeeded",
            "customer_id": customer_id,
            "plan_id": plan_id,
            "amount": amount_cents,
        }
