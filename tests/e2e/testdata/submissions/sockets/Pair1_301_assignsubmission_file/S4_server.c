#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

int main(int argc, char **argv) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    int one = 1;
    setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
    struct sockaddr_in addr = {0};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(atoi(argc > 1 ? argv[1] : "5555"));
    addr.sin_addr.s_addr = inet_addr("127.0.0.1");
    if (bind(fd, (struct sockaddr *)&addr, sizeof addr) < 0) { perror("bind"); return 1; }
    if (listen(fd, 4) < 0) { perror("listen"); return 1; }
    for (int i = 0; i < 2; i++) {
        int c = accept(fd, NULL, NULL);
        if (c < 0) { perror("accept"); return 1; }
        char buf[64] = {0};
        ssize_t n = read(c, buf, sizeof buf - 1);
        printf("server got: %.*s\n", (int)(n > 0 ? n : 0), buf);
        close(c);
    }
    close(fd);
    return 0;
}
