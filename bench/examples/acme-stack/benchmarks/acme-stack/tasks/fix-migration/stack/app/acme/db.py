"""Database connections."""
import psycopg

from . import config


def connect(url=None, timeout=3):
    """A new autocommit connection; timeout bounds the connect, in seconds."""
    return psycopg.connect(url or config.database_url(), connect_timeout=timeout, autocommit=True)
