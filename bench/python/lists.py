# lists: build 100000 elements, then walk them summing.  A Scheme list is a
# chain of pairs; the closest Python is a list, and building it by consing is
# what the Scheme program does, so this builds by appending in reverse and then
# walks with an index, which is the idiomatic Python for the same traversal.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def build(i, acc):
    while i:
        acc.append(i)
        i -= 1
    return acc


def total(l):
    acc = 0
    for x in l:
        acc += x
    return acc


repeat(lambda: total(build(100000, [])), "lists")
