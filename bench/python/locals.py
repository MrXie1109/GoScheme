# locals: the same shape with two locals.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def inner(j, acc, a, b):
    while j:
        acc += a + b
        j -= 1
    return acc


def loop(n):
    a, b = 1, 2
    return inner(n, 0, a, b)


repeat(loop, "locals", 200000)
