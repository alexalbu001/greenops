# Terraform for Lambda + API Gateway v2 (HTTP API)

terraform {
  required_version = "~> 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      terraform = "true"
    }
  }
}

#-------------------------
# IAM Role and Policies
#-------------------------
resource "aws_iam_role" "lambda_exec" {
  name               = "greenops_lambda_exec_role_temp"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
    actions = ["sts:AssumeRole"]
  }
}

resource "aws_iam_role_policy" "lambda_dynamodb_sqs" {
  name   = "greenops_dynamodb_sqs_access"
  role   = aws_iam_role.lambda_exec.id
  policy = data.aws_iam_policy_document.dynamodb_sqs_access.json
}

data "aws_iam_policy_document" "dynamodb_sqs_access" {
  statement {
    effect = "Allow"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:UpdateItem",
      "dynamodb:Query",
      "sqs:SendMessage",
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes"
    ]
    resources = [
      aws_dynamodb_table.greenops_jobs.arn,
      aws_sqs_queue.greenops_queue.arn
    ]
  }
}

resource "aws_iam_role_policy_attachment" "lambda_basic_exec" {
  role       = aws_iam_role.lambda_exec.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy" "bedrock_invoke" {
  name   = "greenops_bedrock_invoke"
  role   = aws_iam_role.lambda_exec.id
  policy = data.aws_iam_policy_document.bedrock_invoke.json
}

data "aws_iam_policy_document" "bedrock_invoke" {
  statement {
    effect = "Allow"
    actions = [
      "bedrock:InvokeModel"
    ]
    resources = ["*"]
  }
}
# DynamoDB table for job tracking
resource "aws_dynamodb_table" "greenops_jobs" {
  name         = "greenops-jobs"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "job_id"

  attribute {
    name = "job_id"
    type = "S"
  }

  ttl {
    attribute_name = "expiration_time"
    enabled        = true
  }
}

# SQS Queue for work items
resource "aws_sqs_queue" "greenops_queue" {
  name                       = "greenops-tasks-queue"
  delay_seconds              = 0
  max_message_size           = 262144
  message_retention_seconds  = 86400
  visibility_timeout_seconds = 300
}

#-------------------------
# Lambda Function
#-------------------------

# Worker Lambda function
resource "aws_lambda_function" "greenops_worker" {
  function_name = "greenops-worker"
  role          = aws_iam_role.lambda_exec.arn
  handler       = "build/worker/bootstrap"
  runtime       = "provided.al2"
  timeout       = 300
  memory_size   = 128

  filename         = var.worker_lambda_zip_path
  source_code_hash = filebase64sha256(var.worker_lambda_zip_path)

  environment {
    variables = {
      EMBED_MODEL_ID  = var.embed_model_id
      GEN_MODEL_ID    = var.gen_model_id
      GEN_PROFILE_ARN = var.gen_profile_arn
      JOBS_TABLE      = aws_dynamodb_table.greenops_jobs.name
    }
  }
}

resource "aws_lambda_event_source_mapping" "sqs_worker_trigger" {
  event_source_arn = aws_sqs_queue.greenops_queue.arn
  function_name    = aws_lambda_function.greenops_worker.function_name
  batch_size       = 1
}

resource "aws_lambda_function" "greenops_api" {
  function_name = "greenops-analyze"
  role          = aws_iam_role.lambda_exec.arn
  handler       = "/build/api/bootstrap"
  runtime       = "provided.al2"
  timeout       = 300

  # Assumes you've built a zip at path
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)

  environment {
    variables = {
      EMBED_MODEL_ID  = var.embed_model_id
      GEN_PROFILE_ARN = var.gen_profile_arn
      GEN_MODEL_ID    = var.gen_model_id
      JOBS_TABLE      = aws_dynamodb_table.greenops_jobs.name
      QUEUE_URL       = aws_sqs_queue.greenops_queue.url
    }
  }
}

#-------------------------
# API Gateway HTTP API
#-------------------------
resource "aws_apigatewayv2_api" "http_api" {
  name          = "greenops-http-api"
  protocol_type = "HTTP"
}

# Lambda integration
resource "aws_apigatewayv2_integration" "lambda_integ" {
  api_id                 = aws_apigatewayv2_api.http_api.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.greenops_api.invoke_arn
  payload_format_version = "2.0"
}

# Route for POST /analyze
resource "aws_apigatewayv2_route" "analyze_route" {
  api_id    = aws_apigatewayv2_api.http_api.id
  route_key = "POST /analyze"
  target    = "integrations/${aws_apigatewayv2_integration.lambda_integ.id}"
}

resource "aws_apigatewayv2_route" "job_results_route" {
  api_id    = aws_apigatewayv2_api.http_api.id
  route_key = "GET /jobs/{id}/results"
  target    = "integrations/${aws_apigatewayv2_integration.lambda_integ.id}"
}


resource "aws_apigatewayv2_route" "job_status_route" {
  api_id    = aws_apigatewayv2_api.http_api.id
  route_key = "GET /jobs/{id}"
  target    = "integrations/${aws_apigatewayv2_integration.lambda_integ.id}"
}

# Default stage
resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http_api.id
  name        = "$default"
  auto_deploy = true
}

# Permit API Gateway to invoke Lambda
resource "aws_lambda_permission" "apigw_invoke" {
  statement_id  = "AllowAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.greenops_api.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.http_api.execution_arn}/*/*"
}

#-------------------------
# Variables
#-------------------------
variable "region" {
  description = "AWS region to deploy into"
  type        = string
  default     = "eu-west-1"
}

variable "lambda_zip_path" {
  description = "Path to the compiled Lambda zip file"
  type        = string
  default     = "./function.zip"
}

variable "worker_lambda_zip_path" {
  description = "Path to the compiled Lambda zip file"
  type        = string
  default     = "./worker.zip"
}

variable "queue_url_output" {
  description = "Output the SQS queue URL"
  type        = bool
  default     = true
}

variable "embed_model_id" {
  description = "Bedrock embedding model ID"
  type        = string
  default     = "amazon.titan-embed-text-v2:0"
}

variable "gen_profile_arn" {
  description = "Bedrock generation inference profile ARN"
  type        = string
  default     = "arn:aws:bedrock:eu-west-1:767048271788:inference-profile/eu.anthropic.claude-3-7-sonnet-20250219-v1:0"
}

variable "gen_model_id" {
  description = "Bedrock generation model ID (fallback)"
  type        = string
  default     = "amazon.titan-tg1-large"
}

#-------------------------
# Outputs
#-------------------------
output "api_endpoint" {
  description = "Invoke URL for the HTTP API"
  value       = aws_apigatewayv2_api.http_api.api_endpoint
}

output "queue_url" {
  description = "URL of the SQS queue"
  value       = aws_sqs_queue.greenops_queue.url
}