package main

import (
	"transcoder/server/internal/models"

	"gorm.io/gen"
)

func main() {
	g := gen.NewGenerator(gen.Config{
		OutPath: "./internal/query",
		Mode:    gen.WithoutContext | gen.WithDefaultQuery | gen.WithQueryInterface,
	})

	g.ApplyBasic(
		models.User{},
		models.File{},
		models.Job{},
		models.JobInput{},
		models.JobOutput{},
		models.JobAttempt{},
		models.OutboxEvent{},
		models.ProviderInvocation{},
		models.Task{},
		models.Workflow{},
	)

	g.Execute()
}
