/* mini-eval: a tiny evaluator over a small tagged AST — the closest thing here
 * to a real program.  Numbers and symbols are tagged ints; an expression is a
 * pair chain, built once and evaluated 5000 times. */
#include "common.h"

enum { T_NUM = 0, T_SYM, T_ADD, T_MUL, T_LET };

typedef struct Expr {
    int tag;
    intptr_t num;      /* T_NUM */
    const char *sym;   /* T_SYM, T_LET's name */
    struct Expr *a, *b, *c;
} Expr;

typedef struct Binding { const char *sym; intptr_t val; struct Binding *next; } Binding;

static Expr *num(intptr_t n) { Expr *e = malloc(sizeof(Expr)); e->tag = T_NUM; e->num = n; return e; }
static Expr *sym(const char *s) { Expr *e = malloc(sizeof(Expr)); e->tag = T_SYM; e->sym = s; return e; }
static Expr *bin(int tag, Expr *a, Expr *b) {
    Expr *e = malloc(sizeof(Expr)); e->tag = tag; e->a = a; e->b = b; return e;
}
static Expr *let(const char *name, Expr *val, Expr *body) {
    Expr *e = malloc(sizeof(Expr)); e->tag = T_LET; e->sym = name; e->a = val; e->b = body; return e;
}

static intptr_t ev(Expr *e, Binding *env) {
    switch (e->tag) {
    case T_NUM: return e->num;
    case T_SYM:
        for (Binding *b = env; b; b = b->next)
            if (b->sym == e->sym) return b->val;
        return 0;
    case T_ADD: return ev(e->a, env) + ev(e->b, env);
    case T_MUL: return ev(e->a, env) * ev(e->b, env);
    default: {
        /* (let x v body) is the fourth element of the list the Scheme program
         * walks, so it is spelled out here as an expression of its own. */
        Binding b = { e->sym, ev(e->a, env), env };
        Expr *body = e->b;
        return ev(body, &b);
    }
    }
}

static intptr_t run_mini(void) {
    /* (add 1 (mul 2 (let x 3 (add x x)))) */
    Expr *x = sym("x");
    Expr *prog = bin(T_ADD, num(1),
                     bin(T_MUL, num(2),
                         let("x", num(3), bin(T_ADD, x, x))));
    intptr_t acc = 0;
    for (intptr_t i = 0; i < 5000; i++) acc += ev(prog, NULL);
    return acc;
}

int main(void) {
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += run_mini();
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("mini-eval", r, elapsed, reps);
    return 0;
}
