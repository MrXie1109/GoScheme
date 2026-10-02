# higher-order: a fold over 20000 elements with a function as f.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def fold(f, init, l):
    for x in l:
        init = f(init, x)
    return init


def build(i):
    acc = []
    while i:
        acc.append(i)
        i -= 1
    return acc


repeat(lambda: fold(lambda a, b: a + b, 0, build(20000)), "higher-order")
