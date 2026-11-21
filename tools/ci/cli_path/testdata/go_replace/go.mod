module example.com/goreplace

go 1.25

require (
	example.com/greet v0.0.0
	github.com/google/uuid v1.3.0
)

replace example.com/greet => ./greet

replace github.com/google/uuid => github.com/google/uuid v1.6.0
