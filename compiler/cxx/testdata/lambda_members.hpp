namespace lambda_members {
struct Argument { int value; };
inline auto factory(int seed) {
    return [sum=seed](int a, int b) mutable -> int { sum += a+b; return sum; };
}
inline auto factory(Argument seed) {
    return [value=seed.value](int a) -> int { return value+a; };
}
using Captured = decltype(factory(0));
using WithRecordArgument = decltype(factory(Argument{0}));
inline auto forwarded(int seed) { return factory(seed); }
using Forwarded = decltype(forwarded(0));
inline auto variable = forwarded(1);
using Variable = decltype(variable);
inline auto literal = [](int a, int b) -> int { return a+b; };
using Literal = decltype(literal);
inline auto generic = [](auto value) { return value; };
using Generic = decltype(generic);
struct Factory {
    static Captured make(int seed) { return factory(seed); }
    auto method(int seed) const { return [seed](int a) -> int { return seed+a; }; }
};
using Member = decltype(Factory{}.method(0));
}
namespace other_lambda_members {
inline auto factory(int seed) { return [seed](double value) -> double { return value+seed; }; }
using Captured = decltype(factory(0));
}
