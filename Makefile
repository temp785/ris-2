.POSIX:

.PHONY: create
create: build apply mongo status

.PHONY: delete
delete:
	kubectl delete namespace hashcrack --ignore-not-found=true

.PHONY: build
build:
	docker build -t manager:latest -f manager/Dockerfile ./manager
	docker build -t worker:latest -f worker/Dockerfile ./worker

.PHONY: apply-k8s
apply:
	kubectl apply -f k8s/

.PHONY: mongo
mongo:
	kubectl wait --for=condition=ready pod/mongo-0 -n hashcrack --timeout=120s
	sleep 10
	kubectl exec -n hashcrack mongo-0 -- mongosh --quiet --eval 'rs.initiate({_id: "rs0", members: [{_id: 0, host: "mongo-0.mongo.hashcrack.svc.cluster.local:27017", priority: 2}, {_id: 1, host: "mongo-1.mongo.hashcrack.svc.cluster.local:27017", priority: 1}, {_id: 2, host: "mongo-2.mongo.hashcrack.svc.cluster.local:27017", priority: 1}]}, {force: true})'
	sleep 5

.PHONY: status
status:
	kubectl get pods -n hashcrack -o wide

.PHONY: scale-workers
scale-workers:
	if [ -z "$(N)" ]; then echo "Использование: make scale-workers N=10"; exit 1; fi
	kubectl scale deployment/worker -n hashcrack --replicas=$(N)
