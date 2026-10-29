struct Pair { int a, b; };
int _fltused;
__declspec(dllimport) int add(int, int), numbered(int, int);
__declspec(dllimport) double mix(double, int), varmix(int, ...);
__declspec(dllimport) struct Pair pair(struct Pair);
__declspec(dllimport) int indirect(const struct Pair *);
int dependency(int, int);
int delay_call(int a, int b) {
    struct Pair p = { a, b }, q = pair(p);
    return add(a,b) + numbered(a,b) + (int)mix(0.5,b) + q.a + q.b
        + indirect(&p) + (int)varmix(5,1.0,2.0,3.0,4.0,5.0)
        + dependency(a,b);
}
