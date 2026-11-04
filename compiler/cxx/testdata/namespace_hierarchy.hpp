#pragma once
#include "hierarchy.hpp"

// Public catalogs deliberately use aliases from a different namespace from the
// actual declarations. A contextual-name prefix cannot resolve this hierarchy.
namespace hierarchy_catalog {
using Counter = example::Counter;
using CounterAlias = Counter;
using Padding = example::Prefix;
using Left = hierarchy::Left;
using Right = hierarchy::Right;
using Hidden = hierarchy::Hidden;
using Secret = hierarchy::Secret;
using PlainLeft = hierarchy::PlainLeft;
using PlainRight = hierarchy::PlainRight;
using IntBox = hierarchy::IntBox;
using Nested = hierarchy::Nested::Base;
namespace extra { struct Unused {}; }
}
namespace hierarchy_catalog_alias = hierarchy_catalog;
namespace hierarchy_empty {}
namespace hierarchy_api {
using Root = hierarchy::Alias;
using Ambiguous = hierarchy::Ambiguous;
using Templated = hierarchy::Templated;
using NestedBase = hierarchy::NestedBase;
using Target = hierarchy::Target;
using Conditional = hierarchy::Conditional;
using Leaf = hierarchy::Leaf;
}
