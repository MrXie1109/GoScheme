# strings: append 3000 single characters.  Python's str is immutable, so this
# is the same quadratic copy the Scheme program does with string-append.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def build():
    acc = ""
    for _ in range(3000):
        acc += "x"
    return acc


repeat(lambda: len(build()), "strings")
