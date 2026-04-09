

curl -s -X POST http://localhost:8081/v1/auth/token -H "Content-Type: application/json" -d '{"device_id":"test-device","user_id":"user123"}'

curl -s -X POST http://localhost:8081/v1/auth/validate -H "Content-Type: application/json" -d '{"token":"ddbe86f28a6e87eb3b1b3aaee2b3af799f0c88f2036c559ccaa00e1d13032103"}'
