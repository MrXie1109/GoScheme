# vectors: fill a 1000-element list and sum it 100 times.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def run():
    v = [0] * 1000
    i = 0
    while i < 1000:
        v[i] = i
        i += 1
    total = 0
    n = 0
    while n < 100:
        i = 0
        while i < 1000:
            total += v[i]
            i += 1
        n += 1
    return total


repeat(run, "vectors")
