package com.example.lumomoviesgeneratorsvc.repositories;

import com.example.lumomoviesgeneratorsvc.models.entities.MovieComment;
import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.stereotype.Repository;

import java.util.UUID;

@Repository
public interface MovieCommentRepository extends JpaRepository<MovieComment, UUID> {
}
