#pragma once
#include "classes_included.hpp"
namespace class_catalog {
struct Forward;
struct Counter {
    int read(int value) const { return value + 2; }
    struct Nested {};
    using Self = Counter;
};
class Access {
    class Private {};
protected:
    struct Protected {};
public:
    struct Public {};
    using View = Private;
};
using Alias = Counter;
typedef Counter OldAlias;
typedef struct { int value; } Anonymous;
using Const = const Counter;
using Pointer = Counter *;
using Reference = Counter &;
using Scalar = int;
using Nothing = void;
using Function = void();
union Union { int value; struct Nested {}; };
struct Incomplete;
template<class T> struct Box { T value; };
using Selected = Box<int>;
template<class T> using Generic = Box<T>;
struct Abstract { virtual int read() = 0; };
inline auto factory() { return [seed = 2](int value) { return seed + value; }; }
using Lambda = decltype(factory());
inline void local() { struct Local {}; }
namespace { struct Hidden {}; using HiddenAlias = Counter; }
namespace child {
struct Child {};
namespace class_catalog { struct Other {}; }
}
struct Forward {};
inline namespace v1 { struct Versioned {}; }
#ifdef CLASS_CATALOG_EXTRA
struct Conditional {};
#endif
}
namespace class_catalog { using Reopened = Counter; }
namespace catalog_alias = class_catalog;
namespace other_class_catalog { struct Decoy {}; }
namespace empty_catalog {}
