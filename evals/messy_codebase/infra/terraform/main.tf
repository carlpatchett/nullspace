resource "aws_rds_cluster_instance" "prod_database_replica" {
  cluster_identifier = "nullspace-aurora-cluster"
  instance_class     = "db.r6g.xlarge"
  engine             = "aurora-postgresql"
  publicly_accessible = false
}
