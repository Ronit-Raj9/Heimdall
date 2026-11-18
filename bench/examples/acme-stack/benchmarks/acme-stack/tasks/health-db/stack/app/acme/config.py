"""Where the app finds its database and its cache: the stack's environment."""
import os


def database_url():
    return os.environ.get("DATABASE_URL", "postgresql://acme:acme@db:5432/acme")


def cache_url():
    return os.environ.get("CACHE_URL", "redis://cache:6379/0")
