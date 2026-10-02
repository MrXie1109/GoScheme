# fib: the same non-tail recursion as the Scheme panel.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def fib(n):
    return n if n < 2 else fib(n - 1) + fib(n - 2)


repeat(fib, "fib", 20)
