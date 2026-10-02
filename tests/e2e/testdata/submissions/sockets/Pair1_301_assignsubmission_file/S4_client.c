/*
 * Sample client that connects to the server and sends its last argument
 * autOScan-engine local e2e fixtures
 *
 * @author: Felipe Trejos（◕‿◕）
 */

#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc < 4) { fprintf(stderr, "usage: ip port msg\n"); return 1; }
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    struct sockaddr_in addr = {0};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(atoi(argv[2]));
    addr.sin_addr.s_addr = inet_addr(argv[1]);
    if (connect(fd, (struct sockaddr *)&addr, sizeof addr) < 0) { perror("connect"); return 1; }
    write(fd, argv[3], strlen(argv[3]));
    printf("client sent: %s\n", argv[3]);
    close(fd);
    return 0;
}
