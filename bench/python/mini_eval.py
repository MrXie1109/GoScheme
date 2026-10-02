# mini-eval: a small evaluator over a tagged tree, 5000 evaluations of the same
# expression.  This is the closest thing to a real program in the set.
import sys
sys.path.insert(0, __file__.rsplit("/", 1)[0])
from common import repeat


def ev(e, env):
    tag = e[0]
    if tag == "num":
        return e[1]
    if tag == "sym":
        return env[e[1]]
    if tag == "add":
        return ev(e[1], env) + ev(e[2], env)
    if tag == "mul":
        return ev(e[1], env) * ev(e[2], env)
    return ev(e[3], dict(env, **{e[1]: ev(e[2], env)}))


# (add 1 (mul 2 (let x 3 (add x x))))
prog = ("add", ("num", 1), ("mul", ("num", 2), ("let", "x", ("num", 3), ("add", ("sym", "x"), ("sym", "x")))))


def run():
    acc = 0
    for _ in range(5000):
        acc += ev(prog, {})
    return acc


repeat(run, "mini-eval")
