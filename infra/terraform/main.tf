terraform {
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "5.84.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true

  endpoints {
    sqs = var.localstack_endpoint
  }
}

resource "aws_sqs_queue" "news_processing_dlq" {
  name                      = "news-processing-dlq${var.queue_name_suffix}"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" "news_processing_queue" {
  name                       = "news-processing-queue${var.queue_name_suffix}"
  visibility_timeout_seconds = 60
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 1209600

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.news_processing_dlq.arn
    maxReceiveCount     = 5
  })
}
