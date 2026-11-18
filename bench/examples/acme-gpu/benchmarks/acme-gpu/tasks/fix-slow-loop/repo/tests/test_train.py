import pytest
import torch

import train
from data import make
from model import MLP

pytestmark = pytest.mark.skipif(not torch.cuda.is_available(), reason="needs a CUDA GPU")


def test_returns_a_trained_mlp(monkeypatch):
    monkeypatch.setattr(train, "EPOCHS", 1)
    x, y = make(3, n=2048)
    model = train.train(x, y, torch.device("cuda"))
    assert isinstance(model, MLP)
    with torch.no_grad():
        acc = (model(torch.from_numpy(x).cuda()).argmax(1).cpu() == torch.from_numpy(y)).float().mean()
    assert acc > 0.3
