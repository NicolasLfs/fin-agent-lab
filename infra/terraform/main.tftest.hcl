run "news_queues_are_configured_for_at_least_once_delivery" {
  command = apply

  variables {
    queue_name_suffix = "-test"
  }

  assert {
    condition     = aws_sqs_queue.news_processing_queue.name == "news-processing-queue-test"
    error_message = "The news processing queue must use its documented name."
  }

  assert {
    condition     = aws_sqs_queue.news_processing_queue.visibility_timeout_seconds == 60
    error_message = "The processing queue must give the worker time to complete."
  }

  assert {
    condition     = aws_sqs_queue.news_processing_dlq.name == "news-processing-dlq-test"
    error_message = "The dead-letter queue must use its documented name."
  }

  assert {
    condition     = jsondecode(aws_sqs_queue.news_processing_queue.redrive_policy).maxReceiveCount == 5
    error_message = "Failed messages must reach the DLQ after five receives."
  }

  assert {
    condition     = jsondecode(aws_sqs_queue.news_processing_queue.redrive_policy).deadLetterTargetArn == aws_sqs_queue.news_processing_dlq.arn
    error_message = "The processing queue must redrive to the configured dead-letter queue."
  }
}
