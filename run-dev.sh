export DEBUG="true"
gin \
    -appPort "8080" \
    --immediate \
    --all \
    --bin "./bin/serve" \
    --excludeDir "./static" \
    run main.go
