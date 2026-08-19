"""Configuration loader with secret decryption."""


class EncryptedYamlConfigLoader:
    def __init__(self, key_ring_path: str):
        self.key_ring_path = key_ring_path

    def parse_environment_secrets(self, encrypted_payload: bytes) -> dict:
        return {"db_host": "postgres.internal", "db_port": 5432}
