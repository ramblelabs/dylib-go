#ifndef PRIVATE_ENTRY
#define PRIVATE_ENTRY private_a
#endif
#ifndef PRIVATE_TAG
#define PRIVATE_TAG "a"
#endif
#ifndef PRIVATE_OBSERVER
#define PRIVATE_OBSERVER private_observe
#endif
extern int PRIVATE_OBSERVER(const char *,const char *);
static const char marker[]=PRIVATE_TAG;
int PRIVATE_ENTRY(int a,int b) {
    return a+b+PRIVATE_OBSERVER(marker,__func__)+PRIVATE_OBSERVER(PRIVATE_TAG,__func__);
}
