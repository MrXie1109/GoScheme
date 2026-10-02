# closures: 20000 closures created and called once each.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def make(n):
    def add(x):
        return x + n
    return add


def run(n):
    acc = 0
    while n:
        acc += make(n)(1)
        n -= 1
    return acc


repeat(run, "closures", 20000)
