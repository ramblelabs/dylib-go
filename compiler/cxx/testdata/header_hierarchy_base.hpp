#pragma once
namespace header_types {
struct Counter {
 int seed;
 inline static int live_count=0;
 explicit Counter(int n):seed(n) { if(n<0) throw n; ++live_count; }
 virtual ~Counter() { --live_count; }
 virtual int score(int n) const { return seed+n; }
 static int live() { return live_count; }
};
}
using GlobalCounter=header_types::Counter;
struct GlobalPad { int padding[4]={1,2,3,4}; };
