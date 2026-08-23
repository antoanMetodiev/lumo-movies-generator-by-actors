package com.example.lumomoviesgeneratorsvc.controllers;

import com.example.lumomoviesgeneratorsvc.services.MoviesGeneratorByActorsService;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.web.bind.annotation.*;

import java.io.IOException;

@RestController
public class MoviesGeneratorByActorController {

    private final MoviesGeneratorByActorsService moviesGeneratorByActorsService;

    @Autowired
    public MoviesGeneratorByActorController(MoviesGeneratorByActorsService moviesGeneratorByActorsService) {
        this.moviesGeneratorByActorsService = moviesGeneratorByActorsService;
    }

    @PostMapping("/add-movies-by-actor/{imdb_id}")
    public void addMoviesByActor(@PathVariable(name = "imdb_id") String imdb_id) {

        try {
            moviesGeneratorByActorsService.addMoviesByActor(imdb_id);
        } catch (Exception e) {
            e.printStackTrace();
        }
    }

    @PostMapping("/search-movies")
    public void searchMovies(@RequestBody String title) throws IOException, InterruptedException {
        System.out.println("====>>> Търся Филми...!!!");
        moviesGeneratorByActorsService.searchForMovies(title);
    }
}