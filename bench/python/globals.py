# globals: a loop adding two module-level names, 200000 times.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat

a = 1
b = 2


def loop(n):
    acc = 0
    while n:
        acc += a + b
        n -= 1
    return acc


repeat(loop, "globals", 200000)
