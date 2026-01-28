build:
	go build -o kubectl-parallel_scale_down main.go
	sudo mv kubectl-parallel_scale_down /usr/local/bin/