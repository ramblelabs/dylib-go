__declspec(dllimport) int first(int, int), second(int, int), numbered(int, int);
int invoke(int a, int b) {
    return first(a, b) + second(a, b) + numbered(a, b);
}
