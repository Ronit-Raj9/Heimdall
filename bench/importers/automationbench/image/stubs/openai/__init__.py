# Offline stand-in for the openai package: model-backed tools never reach the network here.
class OpenAI:
    def __init__(self, *a, **k):
        raise RuntimeError("model-backed tools are offline in this benchmark")
