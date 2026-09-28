/*
 * Reference solution that prints the file named by its first argument
 * autOScan-engine local e2e fixtures
 *
 * @author: Felipe Trejos（◕‿◕）
 */

#include <stdio.h>

int main(int argc, char **argv) {
    FILE *f = fopen(argc > 1 ? argv[1] : "", "r");
    if (!f) {
        printf("no file\n");
        return 1;
    }
    char line[256];
    while (fgets(line, sizeof line, f)) fputs(line, stdout);
    fclose(f);
    return 0;
}
