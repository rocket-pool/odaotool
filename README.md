# odaotool

Rocket Pool Oracle DAO Standalone Tool

This is a tool designed to simulate Rocket Pool's Oracle DAO duties for debug and testing by users that aren't formally part of the Oracle DAO.

The initial prototypes replicate the functionality from the Smartnode but don't use it as a library yet.


## Building

This app is written in `go` and requires you to have a Go development environment set up.

To build it, simply run:

```
go build
```


## Usage

Use the `--ec-endpoint` (`-e`) flag to indicate the RPC URL for your Execution Client (e.g., `http://192.168.1.10:8545`).

Use the `--bn-endpoint` (`-b`) flag to indicate the RPC URL for your Consensus Client (e.g., `http://192.168.1.10:5052`).

Use `--target-block` (`-t`) to pick a specific Execution block to target for simulation (if omitted, odaotool will just use the chain head).


### Price Submission

To simulate RPL price submission, use the `submit-rpl-price` (`p`) command:

```
./odaotool -e http://192.168.1.10:8545 -b http://192.168.1.10:5052 p
```


### Balance Submission

To simulate network balance submission, use the `submit-network-balances` (`b`) command:

```
./odaotool -e http://192.168.1.10:8545 -b http://192.168.1.10:5052 b
```
