package cxx

import (
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
)

// Keep the producer's type extraction intact; only C carriers cross the facade.
// No standard-library traits/target SDK are needed for the discovery probes.
func inferenceTraits() string {
	return fmt.Sprintf(`
template<class A,class B> struct Same { static constexpr bool value=false; };
template<class A> struct Same<A,A> { static constexpr bool value=true; };
template<class T> struct Unqual { using Type=T; };
template<class T> struct Unqual<const T> { using Type=T; };
template<class T> struct Unqual<volatile T> { using Type=T; };
template<class T> struct Unqual<const volatile T> { using Type=T; };
template<class T> struct Pointer { static constexpr bool value=false; };
template<class T> struct Pointer<T*> { static constexpr bool value=true; };
template<unsigned I,class... T> struct At;
template<unsigned I,class H,class... T> struct At<I,H,T...>:At<I-1,T...> {};
template<class H,class... T> struct At<0,H,T...> { using Type=H; };
template<class F> struct Prototype;
template<class R,class... A> struct Prototype<R(*)(A...)> {
 using Result=R;
 static constexpr unsigned count=sizeof...(A);
 template<unsigned I> using Argument=typename At<I,A...>::Type;
};
template<class R,class... A> struct Prototype<R(*)(A...) noexcept>:Prototype<R(*)(A...)> {};
template<class T> constexpr int code() {
 using U=typename Unqual<T>::Type;
 if constexpr (Same<U,void>::value) return %d;
 else if constexpr (Same<U,bool>::value) return sizeof(U)==1 ? %d : -1;
 else if constexpr (Pointer<U>::value) return sizeof(U)==sizeof(void*) ? %d : -1;
 else if constexpr (__is_enum(U)) return code<__underlying_type(U)>();
 else if constexpr (Same<U,char>::value || Same<U,signed char>::value || Same<U,unsigned char>::value ||
  Same<U,short>::value || Same<U,unsigned short>::value || Same<U,int>::value || Same<U,unsigned int>::value ||
  Same<U,long>::value || Same<U,unsigned long>::value || Same<U,long long>::value || Same<U,unsigned long long>::value ||
  Same<U,wchar_t>::value || Same<U,char16_t>::value || Same<U,char32_t>::value) {
  constexpr bool sign=U(-1)<U(0);
  if constexpr (sizeof(U)==1) return sign ? %d : %d;
  else if constexpr (sizeof(U)==2) return sign ? %d : %d;
  else if constexpr (sizeof(U)==4) return sign ? %d : %d;
  else if constexpr (sizeof(U)==8) return sign ? %d : %d;
  else return -1;
 } else if constexpr (Same<U,float>::value) return sizeof(U)==4 ? %d : -1;
 else if constexpr (Same<U,double>::value) return sizeof(U)==8 ? %d : -1;
 else return -1;
}
`, abi.Void, abi.Bool, abi.Pointer, abi.I8, abi.U8, abi.I16, abi.U16, abi.I32, abi.U32, abi.I64, abi.U64, abi.F32, abi.F64)
}
