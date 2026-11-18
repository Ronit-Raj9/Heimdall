import pytest
import torch

from softmax import softmax

pytestmark = pytest.mark.skipif(not torch.cuda.is_available(), reason="needs a CUDA GPU")


@pytest.mark.parametrize("shape", [(1, 1), (4, 10), (256, 1000), (64, 4096)])
def test_matches_torch(shape):
    x = torch.randn(*shape, device="cuda")
    torch.testing.assert_close(softmax(x), torch.softmax(x, dim=1), atol=1e-5, rtol=1e-4)


def test_large_values():
    x = torch.randn(16, 512, device="cuda") * 1e4
    torch.testing.assert_close(softmax(x), torch.softmax(x, dim=1), atol=1e-5, rtol=1e-4)
