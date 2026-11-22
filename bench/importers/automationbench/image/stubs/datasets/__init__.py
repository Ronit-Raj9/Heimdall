# Minimal stand-in for Hugging Face datasets: the domain loaders only call Dataset.from_list.
class Dataset(list):
    @classmethod
    def from_list(cls, rows):
        return cls(rows)

def concatenate_datasets(ds):
    out = Dataset()
    for d in ds:
        out.extend(d)
    return out
