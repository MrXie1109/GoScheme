# callcc: the Scheme program re-enters a continuation 20000 times.  Python has
# no continuations at all; a generator is the nearest thing, and yielding back
# into the loop is what the continuation does.  This is the one row where the
# comparison is about a feature rather than about speed.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def count_to(n):
    i = 0
    v = 0

    def body():
        yield 0                     # the call/cc returns 0 the first time
        while True:
            yield v + 1             # and each re-entry returns v + 1

    k = body()
    v = next(k)
    while True:
        i += 1
        if i < n:
            v = k.send(None)
        else:
            return v


repeat(count_to, "callcc", 20000)
