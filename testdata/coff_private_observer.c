int private_observe(const char *tag,const char *function) {
    return tag[0]==function[8] && tag[1]==0 && function[9]==0 ? 0 : -1000;
}
