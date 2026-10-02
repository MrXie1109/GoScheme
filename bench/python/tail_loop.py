# tail-loop: 200000 iterations of a tail-recursive accumulator.  Python has no
# tail calls, so the accumulator loop is written as a while loop — which is what
# a Python programmer writes, and what the recursion costs is CPython's business
# to answer.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def loop(n):
    acc = 0
    while n:
        acc += n
        n -= 1
    return acc


repeat(loop, "tail-loop", 200000)
