# sort: merge sort of 1000 elements built the same way as the Scheme one
# (i * 7919 mod 2000, consed in reverse).
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def merge(a, b):
    out = []
    i = j = 0
    while i < len(a) and j < len(b):
        if b[j] < a[i]:
            out.append(b[j]); j += 1
        else:
            out.append(a[i]); i += 1
    out.extend(a[i:])
    out.extend(b[j:])
    return out


def msort(l):
    if len(l) < 2:
        return l
    mid = len(l) // 2
    return merge(msort(l[:mid]), msort(l[mid:]))


def build(i, acc):
    while i:
        acc.append((i * 7919) % 2000)
        i -= 1
    return acc


repeat(lambda: msort(build(1000, []))[0], "sort")
