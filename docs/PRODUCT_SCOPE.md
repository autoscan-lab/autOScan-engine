# Product Scope

## What this repository is

A shared grading engine and data-contract layer.

## What this repository is not

- terminal app UI
- desktop GUI
- SSH/session manager
- code editor frontend

## Consumers

1. `autOScan` terminal app
2. future `autOScan Studio` macOS app
3. potential batch/CI tooling

## Why split

1. one source of truth for grading logic
2. lower maintenance cost than duplicated logic
3. faster UI iteration in each client
