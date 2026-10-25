typedef int (*Function)(int,int);
extern long coal_alias_record[2];
extern int coal_alias_function(int,int), alias_stub(int,int);
extern long *alias_got_mid(void), *alias_got_end(void);
extern Function alias_got_function(void);
extern void *alias_indirect[3];
#if defined(__x86_64__)
extern long *alias_got_slot(void);
#else
extern __INT32_TYPE__ alias_got_relative;
#endif
int alias_check(int a,int b) {
    Function interior=(Function)((__UINTPTR_TYPE__)coal_alias_function+8);
    if (coal_alias_record[0]!=11 || coal_alias_record[1]!=31 ||
        alias_got_mid()!=coal_alias_record+1 || alias_got_end()!=coal_alias_record+2 ||
        alias_got_function()!=interior ||
        alias_indirect[0]!=(void*)(coal_alias_record+1) ||
        alias_indirect[1]!=(void*)(coal_alias_record+2) ||
        alias_indirect[2]!=(void*)interior) return -1;
#if defined(__x86_64__)
    if(alias_got_slot()!=coal_alias_record+1) return -2;
#else
    void *slot=(void*)((__UINTPTR_TYPE__)&alias_got_relative+alias_got_relative);
    if(*(long**)slot!=coal_alias_record+1) return -2;
#endif
    if(coal_alias_function(a,b)!=a+b || interior(a,b)!=a+b+2 ||
       alias_got_function()(a,b)!=a+b+2 || alias_stub(a,b)!=a+b+2) return -3;
    return a+b;
}
#ifdef DYLIB_ALIAS_STANDALONE
int main(void) { return alias_check(20,22)==42 ? 0 : 1; }
#endif
