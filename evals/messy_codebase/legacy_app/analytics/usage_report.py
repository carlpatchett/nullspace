"""Usage report aggregator."""


class MonthlyUsageAggregator:
    def __init__(self, tenant_id: str):
        self.tenant_id = tenant_id

    def export_telemetry_metrics(self, start_date: str, end_date: str) -> dict:
        return {
            "tenant_id": self.tenant_id,
            "api_calls": 145000,
            "data_transferred_mb": 4200.5,
        }
